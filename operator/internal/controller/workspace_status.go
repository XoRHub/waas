package controller

// Status: this file owns patching WorkspaceStatus and its conditions
// (Ready, TemplateDrifted). Reconcile computes WHAT to report — phase
// transitions stay inline there — and calls these helpers for HOW the
// status subresource gets written.

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// patchStatus re-fetches the workspace fresh before writing status, avoiding
// resource-version conflicts, and only ever uses the status subresource.
func (r *WorkspaceReconciler) patchStatus(ctx context.Context, ws *waasv1alpha1.Workspace, mutate func(*waasv1alpha1.WorkspaceStatus)) error {
	fresh := &waasv1alpha1.Workspace{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(ws), fresh); err != nil {
		return client.IgnoreNotFound(err)
	}
	mutate(&fresh.Status)
	fresh.Status.ObservedGeneration = fresh.Generation
	// Conditions follow the Kubernetes convention: ObservedGeneration is
	// the generation the condition was last evaluated against — every
	// reconcile evaluates them, so stamp them all.
	for i := range fresh.Status.Conditions {
		fresh.Status.Conditions[i].ObservedGeneration = fresh.Generation
	}
	if err := r.Status().Update(ctx, fresh); err != nil {
		return fmt.Errorf("updating status of workspace %s: %w", ws.Name, err)
	}
	return nil
}

func (r *WorkspaceReconciler) setUnready(ctx context.Context, ws *waasv1alpha1.Workspace, phase waasv1alpha1.WorkspacePhase, reason, message string) error {
	return r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
		st.Phase = phase
		setCondition(st, metav1.ConditionFalse, reason, message)
	})
}

func setCondition(st *waasv1alpha1.WorkspaceStatus, status metav1.ConditionStatus, reason, message string) {
	setTypedCondition(st, waasv1alpha1.ConditionReady, status, reason, message)
}

// setDriftCondition reports docs/adr/0001 drift: True = a pending
// configuration change (template edit or workspace override update)
// awaits the next scale-up boundary or a manual reload. The condition
// type and reason are kept for API compatibility even though the cause
// may be the workspace's own overrides.
func setDriftCondition(st *waasv1alpha1.WorkspaceStatus, drifted bool) {
	if drifted {
		setTypedCondition(st, waasv1alpha1.ConditionTemplateDrifted, metav1.ConditionTrue,
			"TemplateChanged", "the desired configuration (template or overrides) changed since this workspace started; the new shape applies at the next resume or on manual reload (the desktop will restart with updates)")
		return
	}
	setTypedCondition(st, waasv1alpha1.ConditionTemplateDrifted, metav1.ConditionFalse,
		"InSync", "the workload matches its desired configuration")
}

// hasDriftCondition reads the PREVIOUS reconcile's verdict (event
// emission on transitions only).
func hasDriftCondition(ws *waasv1alpha1.Workspace) bool {
	for i := range ws.Status.Conditions {
		c := &ws.Status.Conditions[i]
		if c.Type == waasv1alpha1.ConditionTemplateDrifted {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func setTypedCondition(st *waasv1alpha1.WorkspaceStatus, condType string, status metav1.ConditionStatus, reason, message string) {
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	for i, existing := range st.Conditions {
		if existing.Type == cond.Type {
			if existing.Status == cond.Status && existing.Reason == cond.Reason {
				return
			}
			st.Conditions[i] = cond
			return
		}
	}
	st.Conditions = append(st.Conditions, cond)
}
