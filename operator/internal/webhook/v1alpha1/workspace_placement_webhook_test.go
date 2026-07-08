package v1alpha1

// Placement enforcement tests: a user can only place workloads in their
// own namespace (identity-derived prefix or ownership label), placement
// and workload naming are frozen after creation, workload names cannot
// collide inside a namespace, and reserved metadata keys are rejected for
// every caller.

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func placementTemplate() *waasv1alpha1.WorkspaceTemplate {
	t := tpl()
	t.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-{user}"}
	t.Spec.Overrides = &waasv1alpha1.TemplateOverrides{
		AllowedFields: []waasv1alpha1.OverridableField{waasv1alpha1.FieldPlacement, waasv1alpha1.FieldMetadata},
	}
	return t
}

func TestPlacementOwnNamespaceAllowed(t *testing.T) {
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice" // matches the resolved pattern for alice
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), ws); err != nil {
		t.Fatalf("expected admit, got %v", err)
	}
}

func TestPlacementForeignNamespaceDenied(t *testing.T) {
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-bob" // someone else's namespace
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "PlacementDenied") {
		t.Fatalf("expected PlacementDenied, got %v", err)
	}
}

func TestPlacementExistingOwnedNamespaceAllowed(t *testing.T) {
	// A namespace that does not match the current username-derived prefix
	// but carries this owner's ownership label stays reachable (e.g. the
	// username changed upstream at the IdP).
	owned := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-legacy-alias",
		Labels: map[string]string{waasv1alpha1.LabelOwner: ownerUUID},
	}}
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy(), owned)
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-legacy-alias"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), ws); err != nil {
		t.Fatalf("expected admit through ownership label, got %v", err)
	}
}

func TestPlacementSystemNamespaceDeniedForEveryone(t *testing.T) {
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy())
	for _, bad := range []string{"kube-system", nsName /* the platform namespace */} {
		ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
			w.Spec.TargetNamespace = bad
		})
		// system:masters is a bypass subject; shape rules still apply.
		if _, err := v.ValidateCreate(asCaller("admin", "system:masters"), ws); err == nil {
			t.Errorf("target namespace %q must be denied even for bypass callers", bad)
		}
	}
}

func TestPlacementRequiresOverrideRight(t *testing.T) {
	// Template WITHOUT the placement pattern nor the override right: any
	// explicit target namespace is a deviation → denied.
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice"
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "OverrideNotAllowed") {
		t.Fatalf("expected OverrideNotAllowed, got %v", err)
	}
}

func TestPlacementAndWorkloadNameImmutable(t *testing.T) {
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy())
	oldWS := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice"
		w.Spec.WorkloadName = "cad"
	})

	moved := oldWS.DeepCopy()
	moved.Spec.TargetNamespace = "waas-alice-2"
	if _, err := v.ValidateUpdate(asCaller(apiSA), oldWS, moved); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("targetNamespace must be immutable, got %v", err)
	}

	renamed := oldWS.DeepCopy()
	renamed.Spec.WorkloadName = "cad-2"
	if _, err := v.ValidateUpdate(asCaller(apiSA), oldWS, renamed); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("workloadName must be immutable, got %v", err)
	}
}

func TestWorkloadNameCollisionDenied(t *testing.T) {
	existing := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice"
		w.Spec.WorkloadName = "cad"
	})
	v := newValidator(t, placementTemplate(), catalogImage(),
		defaultPolicy(func(p *waasv1alpha1.WorkspacePolicy) {
			five := int32(5)
			p.Spec.Limits.MaxWorkspaces = &five
		}), existing)

	dup := workspace("w2", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice"
		w.Spec.WorkloadName = "cad"
	})
	_, err := v.ValidateCreate(asCaller(apiSA), dup)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected workload-name collision denial, got %v", err)
	}

	// Same name in ANOTHER namespace is fine.
	elsewhere := workspace("w3", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice-lab"
		w.Spec.WorkloadName = "cad"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), elsewhere); err != nil {
		t.Fatalf("same workload name in another namespace must pass, got %v", err)
	}
}

func TestReservedMetadataKeysDenied(t *testing.T) {
	v := newValidator(t, placementTemplate(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
			Labels: map[string]string{waasv1alpha1.LabelWorkspace: "spoof"},
		}
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved-key denial, got %v", err)
	}
}

// The precedence chain's resolved default is the PLATFORM's decision:
// it must be admitted for everyone — even a shared namespace (the
// built-in "waas-workspace" or a global env pattern), even without the
// "placement" override right. Deviations stay gated.
func TestPlacementResolvedDefaultIsAlwaysAdmitted(t *testing.T) {
	// No placement on the template, no override right, global pattern set.
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	v.DefaultNamespacePattern = "waas-{os}-pool"

	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-linux-pool" // the resolved default
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), ws); err != nil {
		t.Fatalf("the server-resolved default must be admitted, got %v", err)
	}

	// Built-in fallback (no template pattern, no global pattern).
	v.DefaultNamespacePattern = ""
	builtin := workspace("w2", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-workspace"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), builtin); err != nil {
		t.Fatalf("the built-in shared default must be admitted, got %v", err)
	}

	// A DEVIATION from the default still needs the placement right.
	deviant := workspace("w3", func(w *waasv1alpha1.Workspace) {
		w.Spec.TargetNamespace = "waas-alice-lab"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), deviant); err == nil ||
		!strings.Contains(err.Error(), "OverrideNotAllowed") {
		t.Fatalf("deviating from the default must need the placement right, got %v", err)
	}
}
