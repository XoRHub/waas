package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// desktopTemplate serves vnc AND rdp, like ubuntu-xfce: both protocols
// share the container's single session password, so at most one Secret
// may be generated.
func desktopTemplate() *waasv1alpha1.WorkspaceTemplate {
	tpl := linuxTemplate()
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "vnc", Port: 5901, Default: true},
		{Name: "rdp", Port: 3389},
	}
	return tpl
}

func TestDesktopCredentialsGeneratedAndInjected(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, desktopTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// Resolver copy next to the CR, owned by it (GC'd together).
	resolver := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-desktop-marc"}, resolver); err != nil {
		t.Fatalf("expected the resolver secret: %v", err)
	}
	password := secretPassword(resolver)
	if len(password) != 32 {
		t.Fatalf("expected a generated 32-char password, got %q", password)
	}
	if len(resolver.OwnerReferences) != 1 {
		t.Fatal("resolver secret must be owner-referenced to the workspace")
	}

	// One password for the workspace, not one per protocol: the kasm
	// prefix must stay untouched and no second desktop secret may exist.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-kasm-marc"}, &corev1.Secret{}); err == nil {
		t.Fatal("no kasm secret must be generated for a vnc/rdp template")
	}

	// Pod copy named like the workload (teardown sweep), same password.
	podCopy := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, podCopy); err != nil {
		t.Fatalf("expected the pod-namespace secret: %v", err)
	}
	if secretPassword(podCopy) != password {
		t.Fatal("pod copy must hold the same password as the resolver copy")
	}
	if podCopy.Labels[labelWorkspace] != ws.Name {
		t.Fatal("pod copy must carry the workspace content labels")
	}

	// The container reads it through WAAS_DESKTOP_PASSWORD, never a literal value — and
	// exactly once, even with both protocols declared.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("expected desktop deployment: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	var pwEnv *corev1.EnvVar
	seen := 0
	for i := range container.Env {
		if container.Env[i].Name == "WAAS_DESKTOP_PASSWORD" {
			pwEnv = &container.Env[i]
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("expected exactly one WAAS_DESKTOP_PASSWORD entry, got %d", seen)
	}
	if pwEnv.ValueFrom == nil || pwEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected WAAS_DESKTOP_PASSWORD from a secretKeyRef, got %+v", pwEnv)
	}
	if pwEnv.ValueFrom.SecretKeyRef.Name != "ws-marc" || pwEnv.ValueFrom.SecretKeyRef.Key != "password" {
		t.Fatalf("WAAS_DESKTOP_PASSWORD must read the pod-namespace copy, got %+v", pwEnv.ValueFrom.SecretKeyRef)
	}

	// Idempotent: a second pass must not rotate the password.
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-desktop-marc"}, resolver); err != nil {
		t.Fatal(err)
	}
	if secretPassword(resolver) != password {
		t.Fatal("reconcile must not rotate the generated password")
	}

	// Drift on the pod copy converges back on the resolver copy.
	podCopy.Data = map[string][]byte{"password": []byte("tampered")}
	if err := c.Update(ctx, podCopy); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, podCopy); err != nil {
		t.Fatal(err)
	}
	if secretPassword(podCopy) != password {
		t.Fatal("pod copy must converge on the resolver copy")
	}
}

// A legacy template without a protocols block synthesizes a vnc entry and
// must get the generated password too.
func TestDesktopCredentialsGeneratedForSynthesizedVNC(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)

	reconcile(t, r, ws)

	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "waas-desktop-marc"}, &corev1.Secret{}); err != nil {
		t.Fatalf("expected a generated secret for the synthesized vnc protocol: %v", err)
	}
}

func TestDesktopCredentialsSkippedWhenExplicit(t *testing.T) {
	for name, mutate := range map[string]func(*waasv1alpha1.WorkspaceTemplate){
		"literal WAAS_DESKTOP_PASSWORD": func(tpl *waasv1alpha1.WorkspaceTemplate) {
			tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_DESKTOP_PASSWORD", Value: "static-pw"}}
		},
		"credentialsSecretRef": func(tpl *waasv1alpha1.WorkspaceTemplate) {
			for i := range tpl.Spec.Protocols {
				tpl.Spec.Protocols[i].CredentialsSecretRef = "desk-creds"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tpl := desktopTemplate()
			mutate(tpl)
			ws := workspace()
			r, c := newFixture(t, tpl, ws)

			reconcile(t, r, ws)

			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "waas-desktop-marc"}, &corev1.Secret{}); err == nil {
				t.Fatal("no secret must be generated when an explicit source exists")
			}
		})
	}
}

// A kasmvnc entry next to a vnc/rdp one: the kasm mechanism wins — both
// inject VNC_PW and share the pod-copy Secret name, so only one may run.
func TestDesktopCredentialsYieldToKasm(t *testing.T) {
	tpl := desktopTemplate()
	tpl.Spec.Protocols = append(tpl.Spec.Protocols,
		waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-kasm-marc"}, &corev1.Secret{}); err != nil {
		t.Fatalf("expected the kasm secret: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-desktop-marc"}, &corev1.Secret{}); err == nil {
		t.Fatal("the desktop mechanism must yield when kasm generates")
	}
}
