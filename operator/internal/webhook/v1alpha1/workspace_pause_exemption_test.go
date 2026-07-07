package v1alpha1

// Pausing is lifecycle, not governance: a workspace that no longer
// complies (right revoked since creation, image disabled…) must still be
// pausable — denying the pause would keep the non-compliant compute
// RUNNING. Resuming re-runs the full check, unchanged.

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func TestPauseIsExemptFromGovernance(t *testing.T) {
	// The template allows nothing; the live workspace carries a custom
	// sizing (grandfathered: created before the right was revoked).
	running := workspace("w1", func(ws *waasv1alpha1.Workspace) {
		ws.Spec.Resources = &corev1.ResourceRequirements{}
	})
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy(), running)

	// Pausing (only spec.paused flips) must pass despite the unauthorized
	// resources override.
	paused := running.DeepCopy()
	paused.Spec.Paused = true
	if _, err := v.ValidateUpdate(asCaller(apiSA), running, paused); err != nil {
		t.Fatalf("pausing a grandfathered workspace must never be denied: %v", err)
	}

	// Resuming re-acquires compute: the full check runs and denies the
	// unauthorized override.
	resumed := paused.DeepCopy()
	resumed.Spec.Paused = false
	if _, err := v.ValidateUpdate(asCaller(apiSA), paused, resumed); err == nil ||
		!strings.Contains(err.Error(), "OverrideNotAllowed") {
		t.Fatalf("resuming must re-run governance and deny the resources override, got %v", err)
	}

	// Pause combined with ANOTHER spec change is NOT exempt: the ride-along
	// mutation must face governance.
	sneaky := running.DeepCopy()
	sneaky.Spec.Paused = true
	sneaky.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{NodeSelector: map[string]string{"zone": "a"}}
	if _, err := v.ValidateUpdate(asCaller(apiSA), running, sneaky); err == nil ||
		!strings.Contains(err.Error(), "OverrideNotAllowed") {
		t.Fatalf("pause bundled with another spec change must be governed, got %v", err)
	}
}

// TestCreateDeniedUnauthorizedResources pins the non-nil contract at the
// admission door: spec.resources PRESENT requires the "resources" right,
// even with empty/identical values.
func TestCreateDeniedUnauthorizedResources(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(ws *waasv1alpha1.Workspace) {
		ws.Spec.Resources = &corev1.ResourceRequirements{}
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "OverrideNotAllowed") || !strings.Contains(err.Error(), "resources") {
		t.Fatalf("expected resources override denial, got %v", err)
	}
}
