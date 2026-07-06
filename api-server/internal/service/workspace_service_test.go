package service

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

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
