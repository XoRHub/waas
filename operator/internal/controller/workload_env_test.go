package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// TestDesktopEnvDropsOverrideValueFrom pins the render-side guard of
// audit finding #3 (defense in depth behind the webhook): an override
// env entry sourced from a valueFrom reference never reaches the pod
// template — dropped entirely, so it cannot mask the template's own
// entry — while the TEMPLATE's valueFrom entries (the dev-ssh pattern)
// and literal overrides render exactly as before. Covers legacy CRs
// stored before the webhook rule existed.
func TestDesktopEnvDropsOverrideValueFrom(t *testing.T) {
	secretRef := func(name, key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key,
		}}
	}
	ws := &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: "marc-box"}}
	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce"},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			OS: waasv1alpha1.OSLinux,
			Env: []corev1.EnvVar{
				// The admin channel: template valueFrom stays rendered.
				{Name: "WAAS_SSH_AUTHORIZED_KEYS", ValueFrom: secretRef("dev-ssh-credentials", "authorized-keys")},
				{Name: "BASE_VAR", Value: "from-template"},
			},
		},
	}
	ov := &waasv1alpha1.WorkspaceOverrides{Env: []corev1.EnvVar{
		// Exfiltration attempts: every valueFrom source is dropped.
		{Name: "STEAL", ValueFrom: secretRef("waas-postgres", "password")},
		{Name: "BASE_VAR", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		// Literal override: untouched.
		{Name: "HTTP_PROXY", Value: "http://proxy:3128"},
	}}

	byName := map[string]corev1.EnvVar{}
	for _, e := range desktopEnv(ws, tpl, ov) {
		byName[e.Name] = e
	}

	if _, found := byName["STEAL"]; found {
		t.Fatal("override valueFrom entry must never reach the pod template")
	}
	if got := byName["BASE_VAR"]; got.ValueFrom != nil || got.Value != "from-template" {
		t.Fatalf("a dropped valueFrom override must not mask the template entry, got %+v", got)
	}
	if got := byName["HTTP_PROXY"]; got.Value != "http://proxy:3128" {
		t.Fatalf("literal override must render as before, got %+v", got)
	}
	ssh := byName["WAAS_SSH_AUTHORIZED_KEYS"]
	if ssh.ValueFrom == nil || ssh.ValueFrom.SecretKeyRef == nil || ssh.ValueFrom.SecretKeyRef.Name != "dev-ssh-credentials" {
		t.Fatalf("template valueFrom (dev-ssh pattern) must keep rendering, got %+v", ssh)
	}
}
