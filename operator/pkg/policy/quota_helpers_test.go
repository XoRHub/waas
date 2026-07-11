package policy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func TestIdentityOf(t *testing.T) {
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			waasv1alpha1.AnnotationUsername: "marc",
			waasv1alpha1.AnnotationGroups:   " dev, ops ,,",
		}},
		Spec: waasv1alpha1.WorkspaceSpec{Owner: "uuid-1"},
	}
	id := IdentityOf(ws)
	if id.Owner != "uuid-1" || id.Username != "marc" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "dev" || id.Groups[1] != "ops" {
		t.Fatalf("groups must be trimmed and empties dropped: %v", id.Groups)
	}

	// Direct-kubectl workspace: no annotations. Groups stay empty (the
	// stricter reading) and the owner UUID doubles as the username.
	bare := &waasv1alpha1.Workspace{Spec: waasv1alpha1.WorkspaceSpec{Owner: "uuid-2"}}
	id = IdentityOf(bare)
	if id.Username != "uuid-2" || len(id.Groups) != 0 {
		t.Fatalf("bare workspace identity: %+v", id)
	}
}

func TestDenialError(t *testing.T) {
	d := denyf(ReasonQuotaExceeded, "max %d workspaces", 3)
	if d.Error() != "max 3 workspaces" || d.Reason != ReasonQuotaExceeded {
		t.Fatalf("unexpected denial: %+v", d)
	}
}

func TestClipboardOf(t *testing.T) {
	no := false
	cases := []struct {
		name        string
		pol         *waasv1alpha1.WorkspacePolicy
		copy, paste bool
	}{
		{"nil policy fails closed", nil, false, false},
		{"absent block allows both", &waasv1alpha1.WorkspacePolicy{}, true, true},
		{"explicit denies stick", &waasv1alpha1.WorkspacePolicy{
			Spec: waasv1alpha1.WorkspacePolicySpec{Clipboard: &waasv1alpha1.ClipboardPolicy{
				CopyFromWorkspace: &no,
			}},
		}, false, true},
	}
	for _, tc := range cases {
		if c, p := ClipboardOf(tc.pol); c != tc.copy || p != tc.paste {
			t.Errorf("%s: got copy=%v paste=%v, want %v/%v", tc.name, c, p, tc.copy, tc.paste)
		}
	}
}

func TestTemplatesByName(t *testing.T) {
	items := []waasv1alpha1.WorkspaceTemplate{
		{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
	}
	byName := TemplatesByName(items)
	if len(byName) != 2 || byName["a"] == nil || byName["b"] == nil {
		t.Fatalf("unexpected map: %v", byName)
	}
	if byName["a"] != &items[0] {
		t.Fatal("must point at the slice elements, not copies")
	}
}

func TestOwnerLoads(t *testing.T) {
	now := metav1.Now()
	tpl := waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "tpl"},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			},
			HomeSize: qty("20Gi"),
		},
	}
	mkWS := func(name, owner, ref string) waasv1alpha1.Workspace {
		return waasv1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       waasv1alpha1.WorkspaceSpec{Owner: owner, TemplateRef: ref},
		}
	}
	deleting := mkWS("dying", "me", "tpl")
	deleting.DeletionTimestamp = &now

	workspaces := []waasv1alpha1.Workspace{
		mkWS("mine", "me", "tpl"),
		mkWS("orphan", "me", "vanished"), // template gone
		mkWS("under-admission", "me", "tpl"),
		mkWS("theirs", "other", "tpl"),
		deleting,
	}
	loads := OwnerLoads(workspaces, "me", "under-admission",
		TemplatesByName([]waasv1alpha1.WorkspaceTemplate{tpl}), nil)

	if len(loads) != 2 {
		t.Fatalf("want 2 loads (excluded/other-owner/deleting skipped), got %d: %+v", len(loads), loads)
	}
	if loads[0].Storage.String() != "20Gi" {
		t.Fatalf("templated workspace storage: %+v", loads[0])
	}
	// The vanished-template fallback still weighs the home volume.
	if loads[1].Storage.String() != DefaultHomeSize || !loads[1].CPU.IsZero() {
		t.Fatalf("orphan fallback must be storage-only DefaultHomeSize: %+v", loads[1])
	}
}

func TestRetainedVolumeLoads(t *testing.T) {
	now := metav1.Now()
	mkPVC := func(ns, name, size string) corev1.PersistentVolumeClaim {
		return corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
				},
			},
		}
	}
	deleting := mkPVC("ns1", "going", "5Gi")
	deleting.DeletionTimestamp = &now

	loads := RetainedVolumeLoads([]corev1.PersistentVolumeClaim{
		mkPVC("ns1", "kept", "10Gi"),
		mkPVC("ns1", "adopted", "15Gi"), // being reattached: excluded
		deleting,
	}, types.NamespacedName{Namespace: "ns1", Name: "adopted"})

	if len(loads) != 1 {
		t.Fatalf("want 1 load (adopted + deleting skipped), got %d", len(loads))
	}
	l := loads[0]
	if l.Storage.String() != "10Gi" || !l.Detached || !l.Paused {
		t.Fatalf("retained volumes are storage-only detached loads: %+v", l)
	}
}

func TestRemoteWorkspacesAllowed(t *testing.T) {
	if RemoteWorkspacesAllowed(nil) {
		t.Fatal("nil policy must fail closed")
	}
	if RemoteWorkspacesAllowed(&waasv1alpha1.WorkspacePolicy{}) {
		t.Fatal("default must be denied")
	}
	if !RemoteWorkspacesAllowed(&waasv1alpha1.WorkspacePolicy{
		Spec: waasv1alpha1.WorkspacePolicySpec{RemoteWorkspaces: true},
	}) {
		t.Fatal("opt-in must be allowed")
	}
}

func TestNonEmpty(t *testing.T) {
	if nonEmpty("a", "b") != "a" || nonEmpty("", "b") != "b" {
		t.Fatal("nonEmpty precedence broken")
	}
}
