package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func dockerSecret(name string, payload string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(payload)},
	}
}

func privateCatalogEntry() *waasv1alpha1.WorkspaceImage {
	img := catalogEntry(true)
	img.Spec.ImagePullSecretRef = "acme-pull"
	return img
}

func TestPullSecretCopiedAndWiredForPlacedWorkspace(t *testing.T) {
	ws := placedWorkspace()
	// The fixture workspace deviates on placement: the template must
	// delegate that field or governance (rightly) denies it.
	tpl := linuxTemplate()
	tpl.Spec.Overrides = &waasv1alpha1.TemplateOverrides{
		AllowedFields: []waasv1alpha1.OverridableField{waasv1alpha1.FieldPlacement},
	}
	r, c := newFixture(t, tpl, ws, privateCatalogEntry(), openPolicy(nil),
		dockerSecret("acme-pull", `{"auths":{"reg.acme":{"auth":"djE="}}}`))
	ctx := context.Background()

	reconcile(t, r, ws)

	// Namespace-local copy, shared name, pull-secret label, NO workspace label.
	copySecret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "waas-pull-acme-pull"}, copySecret); err != nil {
		t.Fatalf("expected the pull-secret copy in the target namespace: %v", err)
	}
	if copySecret.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("copy must keep the source type, got %s", copySecret.Type)
	}
	if copySecret.Labels[waasv1alpha1.LabelPullSecret] != "true" {
		t.Fatal("copy must carry the pull-secret label (janitor exclusion)")
	}
	if copySecret.Labels[labelWorkspace] != "" {
		t.Fatal("copy is shared by the namespace: it must not be workspace content")
	}

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station"}, dep); err != nil {
		t.Fatal(err)
	}
	pulls := dep.Spec.Template.Spec.ImagePullSecrets
	if len(pulls) != 1 || pulls[0].Name != "waas-pull-acme-pull" {
		t.Fatalf("PodSpec must reference the namespace-local copy, got %+v", pulls)
	}

	// Rotation on the source converges the copy.
	source := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "acme-pull"}, source); err != nil {
		t.Fatal(err)
	}
	source.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{"reg.acme":{"auth":"djI="}}}`)
	if err := c.Update(ctx, source); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "waas-pull-acme-pull"}, copySecret); err != nil {
		t.Fatal(err)
	}
	if string(copySecret.Data[corev1.DockerConfigJsonKey]) != `{"auths":{"reg.acme":{"auth":"djI="}}}` {
		t.Fatal("rotated source must propagate to the copy")
	}
}

func TestPullSecretUnplacedUsesSourceDirectly(t *testing.T) {
	ws := workspace() // unplaced: pod lands next to the source secret
	r, c := newFixture(t, linuxTemplate(), ws, privateCatalogEntry(), openPolicy(nil),
		dockerSecret("acme-pull", `{"auths":{}}`))
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-pull-acme-pull"}, &corev1.Secret{}); err == nil {
		t.Fatal("no copy must be made when the pod shares the source's namespace")
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	pulls := dep.Spec.Template.Spec.ImagePullSecrets
	if len(pulls) != 1 || pulls[0].Name != "acme-pull" {
		t.Fatalf("PodSpec must reference the source directly, got %+v", pulls)
	}
}

func TestPullSecretMissingFailsClosed(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws, privateCatalogEntry(), openPolicy(nil))
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, &appsv1.Deployment{}); err == nil {
		t.Fatal("no compute may be created while the pull secret is unresolvable")
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseFailed {
		t.Fatalf("expected PhaseFailed, got %s", got.Status.Phase)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != string(ReasonPullSecretMissing) {
		t.Fatalf("expected PullSecretMissing condition, got %+v", got.Status.Conditions)
	}
}
