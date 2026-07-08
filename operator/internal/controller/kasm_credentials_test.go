package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func kasmTemplate() *waasv1alpha1.WorkspaceTemplate {
	tpl := linuxTemplate()
	tpl.Name = "kasm-firefox"
	tpl.Spec.Image = "docker.io/kasmweb/firefox:1.19.0"
	tpl.Spec.HomeMountPath = "/home/kasm-user"
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "kasmvnc", Port: 6901, Default: true},
	}
	return tpl
}

func TestKasmCredentialsGeneratedAndInjected(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	r, c := newFixture(t, kasmTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// Resolver copy next to the CR, owned by it (GC'd together).
	resolver := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-kasm-marc"}, resolver); err != nil {
		t.Fatalf("expected the resolver secret: %v", err)
	}
	password := secretPassword(resolver)
	if len(password) != 32 {
		t.Fatalf("expected a generated 32-char password, got %q", password)
	}
	if len(resolver.OwnerReferences) != 1 {
		t.Fatal("resolver secret must be owner-referenced to the workspace")
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

	// The container reads it through VNC_PW, never a literal value.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("expected desktop deployment: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	var vncPW *corev1.EnvVar
	for i := range container.Env {
		if container.Env[i].Name == "VNC_PW" {
			vncPW = &container.Env[i]
		}
	}
	if vncPW == nil || vncPW.ValueFrom == nil || vncPW.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected VNC_PW from a secretKeyRef, got %+v", vncPW)
	}
	if vncPW.ValueFrom.SecretKeyRef.Name != "ws-marc" || vncPW.ValueFrom.SecretKeyRef.Key != "password" {
		t.Fatalf("VNC_PW must read the pod-namespace copy, got %+v", vncPW.ValueFrom.SecretKeyRef)
	}
	if got := container.VolumeMounts[0].MountPath; got != "/home/kasm-user" {
		t.Fatalf("expected the template's homeMountPath, got %q", got)
	}
	if container.Ports[0].ContainerPort != 6901 {
		t.Fatalf("expected the kasmvnc port, got %d", container.Ports[0].ContainerPort)
	}

	// Idempotent: a second pass must not rotate the password.
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-kasm-marc"}, resolver); err != nil {
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

func TestKasmCredentialsSkippedWhenExplicit(t *testing.T) {
	tpl := kasmTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: "VNC_PW", Value: "static-pw"}}
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-kasm-marc"}, &corev1.Secret{}); err == nil {
		t.Fatal("no secret must be generated when the template sets VNC_PW")
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "VNC_PW" && env.ValueFrom != nil {
			t.Fatal("explicit VNC_PW must stay a literal, not a secretKeyRef")
		}
	}
}
