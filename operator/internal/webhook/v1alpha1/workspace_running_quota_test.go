package v1alpha1

// maxRunningWorkspaces guards the two transitions INTO compute — create
// of a non-paused workspace and resume — while pause stays exempt and
// paused workspaces never occupy a slot. Independent from maxWorkspaces
// (ownership), which counts paused workspaces too.

import (
	"strings"
	"testing"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func runningQuotaPolicy() *waasv1alpha1.WorkspacePolicy {
	return defaultPolicy(func(p *waasv1alpha1.WorkspacePolicy) {
		one, five := int32(1), int32(5)
		p.Spec.Limits.MaxWorkspaces = &five
		p.Spec.Limits.MaxRunningWorkspaces = &one
	})
}

func TestCreateDeniedWhenRunningQuotaReached(t *testing.T) {
	// One workspace running, well under maxWorkspaces=5: only the
	// running quota can deny — proving the two limits are independent.
	v := newValidator(t, tpl(), catalogImage(), runningQuotaPolicy(), workspace("w1"))

	_, err := v.ValidateCreate(asCaller(apiSA), workspace("w2"))
	if err == nil || !strings.Contains(err.Error(), "QuotaExceeded") ||
		!strings.Contains(err.Error(), "running workspace quota") {
		t.Fatalf("expected running quota denial, got %v", err)
	}

	// Creating paused takes no slot and must be admitted.
	pausedNew := workspace("w2", func(ws *waasv1alpha1.Workspace) { ws.Spec.Paused = true })
	if _, err := v.ValidateCreate(asCaller(apiSA), pausedNew); err != nil {
		t.Fatalf("create-paused must not consume a running slot: %v", err)
	}
}

func TestResumeDeniedWhenRunningQuotaReached(t *testing.T) {
	pausedWS := workspace("w-paused", func(ws *waasv1alpha1.Workspace) { ws.Spec.Paused = true })

	// Another workspace holds the single running slot: resume denied.
	v := newValidator(t, tpl(), catalogImage(), runningQuotaPolicy(), workspace("w-running"), pausedWS)
	resumed := pausedWS.DeepCopy()
	resumed.Spec.Paused = false
	_, err := v.ValidateUpdate(asCaller(apiSA), pausedWS, resumed)
	if err == nil || !strings.Contains(err.Error(), "QuotaExceeded") ||
		!strings.Contains(err.Error(), "running workspace quota") {
		t.Fatalf("expected running quota denial on resume, got %v", err)
	}

	// The sibling paused instead: the slot is free, resume admitted.
	otherPaused := workspace("w-running", func(ws *waasv1alpha1.Workspace) { ws.Spec.Paused = true })
	v = newValidator(t, tpl(), catalogImage(), runningQuotaPolicy(), otherPaused, pausedWS)
	if _, err := v.ValidateUpdate(asCaller(apiSA), pausedWS, resumed); err != nil {
		t.Fatalf("resume with a free slot must be admitted: %v", err)
	}
}

func TestPauseExemptFromRunningQuota(t *testing.T) {
	// Two workspaces running over the limit (grandfathered): pausing one
	// only frees compute and must stay exempt.
	w1 := workspace("w1")
	v := newValidator(t, tpl(), catalogImage(), runningQuotaPolicy(), w1, workspace("w2"))
	paused := w1.DeepCopy()
	paused.Spec.Paused = true
	if _, err := v.ValidateUpdate(asCaller(apiSA), w1, paused); err != nil {
		t.Fatalf("pausing must never be denied by the running quota: %v", err)
	}
}
