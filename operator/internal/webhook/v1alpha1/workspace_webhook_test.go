package v1alpha1

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

const (
	nsName    = "default"
	apiSA     = "system:serviceaccount:waas:waas-api-server"
	imageRef  = "registry.xorhub.io/waas/ubuntu-xfce:1.0.0"
	ownerUUID = "uuid-alice"
)

func newValidator(t *testing.T, objs ...client.Object) *WorkspaceValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := waasv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &WorkspaceValidator{
		Client:         c,
		TrustedWriters: []string{apiSA},
		BypassSubjects: []string{"system:masters"},
	}
}

// asCaller builds the admission context carrying the caller identity.
func asCaller(username string, groups ...string) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: username, Groups: groups},
		},
	})
}

func tpl() *waasv1alpha1.WorkspaceTemplate {
	return &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: nsName},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "XFCE",
			OS:          waasv1alpha1.OSLinux,
			Image:       imageRef,
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
}

func catalogImage(mutators ...func(*waasv1alpha1.WorkspaceImage)) *waasv1alpha1.WorkspaceImage {
	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "ubuntu-xfce", Namespace: nsName},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XFCE",
			Image:       imageRef,
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolVNC},
			Enabled:     true,
		},
	}
	for _, m := range mutators {
		m(img)
	}
	return img
}

func defaultPolicy(mutators ...func(*waasv1alpha1.WorkspacePolicy)) *waasv1alpha1.WorkspacePolicy {
	two := int32(2)
	p := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: nsName},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Priority: 0,
			Limits:   waasv1alpha1.PolicyLimits{MaxWorkspaces: &two},
		},
	}
	for _, m := range mutators {
		m(p)
	}
	return p
}

func workspace(name string, mutators ...func(*waasv1alpha1.Workspace)) *waasv1alpha1.Workspace {
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: nsName,
			Annotations: map[string]string{
				waasv1alpha1.AnnotationUsername: "alice",
				waasv1alpha1.AnnotationGroups:   "nymphe:users",
			},
		},
		Spec: waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: ownerUUID},
	}
	for _, m := range mutators {
		m(ws)
	}
	return ws
}

func TestCreateAllowedViaTrustedWriter(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	if _, err := v.ValidateCreate(asCaller(apiSA), workspace("w1")); err != nil {
		t.Fatalf("expected admit, got %v", err)
	}
}

func TestCreateDeniedWhenQuotaReached(t *testing.T) {
	one := int32(1)
	pol := defaultPolicy(func(p *waasv1alpha1.WorkspacePolicy) {
		p.Spec.Limits.MaxWorkspaces = &one
	})
	existing := workspace("w1")
	v := newValidator(t, tpl(), catalogImage(), pol, existing)

	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w2"))
	if err == nil || !strings.Contains(err.Error(), "QuotaExceeded") || !strings.Contains(err.Error(), "(1/1)") {
		t.Fatalf("expected explicit quota denial, got: %v", err)
	}
}

func TestCreateDeniedImageNotInCatalog(t *testing.T) {
	v := newValidator(t, tpl(), defaultPolicy()) // empty catalog
	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w1"))
	if err == nil || !strings.Contains(err.Error(), "ImageNotInCatalog") {
		t.Fatalf("expected catalog denial, got: %v", err)
	}
}

func TestCreateDeniedImageDisabled(t *testing.T) {
	img := catalogImage(func(i *waasv1alpha1.WorkspaceImage) { i.Spec.Enabled = false })
	v := newValidator(t, tpl(), img, defaultPolicy())
	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w1"))
	if err == nil || !strings.Contains(err.Error(), "ImageDisabled") {
		t.Fatalf("expected disabled-image denial, got: %v", err)
	}
}

func TestCreateDeniedImageRestrictedToOtherGroup(t *testing.T) {
	img := catalogImage(func(i *waasv1alpha1.WorkspaceImage) {
		i.Spec.AllowedGroups = []string{"nymphe:dev"}
	})
	v := newValidator(t, tpl(), img, defaultPolicy())
	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w1")) // alice is in nymphe:users
	if err == nil || !strings.Contains(err.Error(), "ImageNotAllowed") {
		t.Fatalf("expected group denial, got: %v", err)
	}
}

func TestMultiGroupHighestPriorityPolicyWins(t *testing.T) {
	// alice is in users+dev; dev policy (higher prio) allows 5, default 1.
	one, five := int32(1), int32(5)
	def := defaultPolicy(func(p *waasv1alpha1.WorkspacePolicy) { p.Spec.Limits.MaxWorkspaces = &one })
	dev := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "power-user", Namespace: nsName},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Priority: 100,
			Subjects: []waasv1alpha1.PolicySubject{{Kind: waasv1alpha1.SubjectGroup, Name: "nymphe:dev"}},
			Limits:   waasv1alpha1.PolicyLimits{MaxWorkspaces: &five},
		},
	}
	existing := workspace("w1")
	v := newValidator(t, tpl(), catalogImage(), def, dev, existing)

	multi := workspace("w2", func(w *waasv1alpha1.Workspace) {
		w.Annotations[waasv1alpha1.AnnotationGroups] = "nymphe:users,nymphe:dev"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), multi); err != nil {
		t.Fatalf("power-user policy should apply (5 max), got: %v", err)
	}

	// Same user without the dev group: default policy (1 max) denies.
	if _, err := v.ValidateCreate(asCaller(apiSA), workspace("w3")); err == nil {
		t.Fatal("default policy should deny the second workspace")
	}
}

func TestNoMatchingPolicyFailsClosed(t *testing.T) {
	devOnly := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-only", Namespace: nsName},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Subjects: []waasv1alpha1.PolicySubject{{Kind: waasv1alpha1.SubjectGroup, Name: "nymphe:dev"}},
		},
	}
	v := newValidator(t, tpl(), catalogImage(), devOnly)
	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w1"))
	if err == nil || !strings.Contains(err.Error(), "NoPolicyMatches") {
		t.Fatalf("expected fail-closed denial, got: %v", err)
	}
}

func TestDirectKubectlIdentityRules(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())

	// Forged identity annotations from an untrusted caller: denied.
	forged := workspace("w1") // carries annotations, caller is not the api-server
	forged.Spec.Owner = "alice"
	_, err := v.ValidateCreate(asCaller("alice", "system:authenticated"), forged)
	if err == nil || !strings.Contains(err.Error(), "IdentityViolation") {
		t.Fatalf("expected identity denial for forged annotations, got: %v", err)
	}

	// Owner not matching the authenticated user: denied.
	spoofed := workspace("w2", func(w *waasv1alpha1.Workspace) { w.Annotations = nil })
	spoofed.Spec.Owner = "bob"
	_, err = v.ValidateCreate(asCaller("alice", "system:authenticated"), spoofed)
	if err == nil || !strings.Contains(err.Error(), "IdentityViolation") {
		t.Fatalf("expected owner-mismatch denial, got: %v", err)
	}

	// Clean direct create with owner == username: allowed by the
	// subjects-less default policy.
	clean := workspace("w3", func(w *waasv1alpha1.Workspace) {
		w.Annotations = nil
		w.Spec.Owner = "alice"
	})
	if _, err := v.ValidateCreate(asCaller("alice", "system:authenticated"), clean); err != nil {
		t.Fatalf("expected kubectl create to pass under default policy, got: %v", err)
	}
}

func TestOwnerImmutableOnUpdate(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	oldWS := workspace("w1")
	newWS := workspace("w1")
	newWS.Spec.Owner = "uuid-mallory"
	_, err := v.ValidateUpdate(asCaller(apiSA), oldWS, newWS)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected owner immutability denial, got: %v", err)
	}
}

func TestGrandfatheringSkipsUnchangedSpec(t *testing.T) {
	// No catalog, no policy: a spec-identical update (metadata churn)
	// must pass — pre-governance workspaces keep working.
	v := newValidator(t, tpl())
	oldWS := workspace("w1")
	newWS := workspace("w1")
	newWS.Labels = map[string]string{"argocd.argoproj.io/instance": "waas"}
	if _, err := v.ValidateUpdate(asCaller(apiSA), oldWS, newWS); err != nil {
		t.Fatalf("metadata-only update must not trigger policy, got: %v", err)
	}

	// But a spec change on the same pre-governance workspace enforces.
	resized := workspace("w1")
	resized.Spec.Resources = &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
	}
	if _, err := v.ValidateUpdate(asCaller(apiSA), oldWS, resized); err == nil {
		t.Fatal("spec change must enforce policy (none matches here → deny)")
	}
}

func TestBypassSubjectsSkipPolicy(t *testing.T) {
	v := newValidator(t, tpl()) // no catalog/policy at all
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) { w.Annotations = nil })
	warns, err := v.ValidateCreate(asCaller("admin", "system:masters"), ws)
	if err != nil {
		t.Fatalf("system:masters should bypass policy, got: %v", err)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "bypassed") {
		t.Fatalf("bypass must be surfaced as a warning, got: %v", warns)
	}
}

func TestProtocolMismatchDenied(t *testing.T) {
	rdpOnly := catalogImage(func(i *waasv1alpha1.WorkspaceImage) {
		i.Spec.Protocols = []waasv1alpha1.Protocol{waasv1alpha1.ProtocolRDP}
	})
	v := newValidator(t, tpl(), rdpOnly, defaultPolicy())
	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w1"))
	if err == nil || !strings.Contains(err.Error(), "ProtocolMismatch") {
		t.Fatalf("expected protocol denial, got: %v", err)
	}
}
