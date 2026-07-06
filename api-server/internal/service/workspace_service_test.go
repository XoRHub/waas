package service

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// TestLegacyTemplateAlwaysExposesAConnection is the non-regression test
// for "dev firefox template shows an empty connection field": a template
// with no protocols block (legacy, OS-derived) must still surface exactly
// one default protocol in its API projection — the UI always has a
// connection to display, never a silent empty box.
func TestLegacyTemplateAlwaysExposesAConnection(t *testing.T) {
	tpl := &waasv1alpha1.WorkspaceTemplate{
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "Firefox (isolated browsing)",
			OS:          waasv1alpha1.OSLinux,
			Image:       "reg/firefox:1",
			Port:        5901,
			// No Protocols: the legacy shape that triggered the bug.
		},
	}
	m := templateToModel(tpl)
	if len(m.Protocols) != 1 {
		t.Fatalf("legacy template must synthesize exactly one protocol, got %+v", m.Protocols)
	}
	p := m.Protocols[0]
	if p.Name != "vnc" || p.Port != 5901 || !p.Default {
		t.Fatalf("expected default vnc:5901, got %+v", p)
	}

	// Windows legacy templates derive rdp:3389 the same way.
	tpl.Spec.OS = waasv1alpha1.OSWindows
	tpl.Spec.Port = 0
	m = templateToModel(tpl)
	if len(m.Protocols) != 1 || m.Protocols[0].Name != "rdp" || m.Protocols[0].Port != 3389 {
		t.Fatalf("expected default rdp:3389 for windows, got %+v", m.Protocols)
	}
}

// TestOverridesSummaryIsAuditSafe pins the audit contract: field names
// and env var NAMES appear, env VALUES never do (they may carry
// credentials like VNC_PW).
func TestOverridesSummaryIsAuditSafe(t *testing.T) {
	if got := overridesSummary(nil); got != "" {
		t.Fatalf("nil overrides must produce no audit line, got %q", got)
	}
	if got := overridesSummary(&waasv1alpha1.WorkspaceOverrides{}); got != "" {
		t.Fatalf("empty overrides must produce no audit line, got %q", got)
	}

	ov := &waasv1alpha1.WorkspaceOverrides{
		Env: []corev1.EnvVar{
			{Name: "VNC_PW", Value: "super-secret"},
			{Name: "HTTP_PROXY", Value: "http://proxy:3128"},
		},
		Protocol:     "rdp",
		NodeSelector: map[string]string{"zone": "a"},
	}
	got := overridesSummary(ov)
	for _, want := range []string{"env=VNC_PW,HTTP_PROXY", "protocol=rdp", "nodeSelector"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q must contain %q", got, want)
		}
	}
	if strings.Contains(got, "super-secret") || strings.Contains(got, "proxy:3128") {
		t.Fatalf("summary %q leaks env values", got)
	}
}
