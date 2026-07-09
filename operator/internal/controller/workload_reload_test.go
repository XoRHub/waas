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

// TestManualReloadForcesBoundaryConvergence pins the manual-reload
// semantics (docs/adr/0001, manual reload note): the reload annotation
// forces ONE immediate convergence boundary on a RUNNING workload — the
// pending template applies now, the annotation is consumed, drift clears
// — and a request with nothing to apply is consumed without a restart.
func TestManualReloadForcesBoundaryConvergence(t *testing.T) {
	tpl := linuxTemplate()
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// Make the workload report running so the boundary rule applies.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Status.ReadyReplicas = 1
	if err := c.Status().Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	hash := dep.Spec.Template.Annotations[annotationPodTemplateHash]

	// Reload with NOTHING pending: the one-shot request is consumed
	// silently, the running workload does not roll.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	ws.Annotations = map[string]string{waasv1alpha1.AnnotationReloadRequestedAt: "2026-07-10T08:00:00Z"}
	if err := c.Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.Annotations[waasv1alpha1.AnnotationReloadRequestedAt]; ok {
		t.Fatal("a reload request with no pending drift must still be consumed")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationPodTemplateHash] != hash {
		t.Fatal("a no-op reload must not roll the workload")
	}

	// Template edit MID-SESSION: drift is reported, never applied.
	tpl.Spec.Image = "ghcr.io/xorhub/waas/desktop-xfce:v2"
	if err := c.Update(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationPodTemplateHash] != hash {
		t.Fatal("a running workload must NOT roll on a template edit (boundary convergence)")
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if cond := findCondition(got, waasv1alpha1.ConditionTemplateDrifted); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TemplateDrifted=True while running, got %+v", cond)
	}

	// Manual reload: the api-server stamps the annotation; the reconcile
	// applies the pending shape NOW without touching spec.paused or the
	// manual-state-at annotation, then consumes the request.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	if ws.Annotations == nil {
		ws.Annotations = map[string]string{}
	}
	ws.Annotations[waasv1alpha1.AnnotationReloadRequestedAt] = "2026-07-10T09:00:00Z"
	if err := c.Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationPodTemplateHash] == hash {
		t.Fatal("a manual reload must converge the pod template immediately")
	}
	if img := dep.Spec.Template.Spec.Containers[0].Image; img != "ghcr.io/xorhub/waas/desktop-xfce:v2" {
		t.Fatalf("expected the updated image after reload, got %s", img)
	}
	if want := int32(1); dep.Spec.Replicas == nil || *dep.Spec.Replicas != want {
		t.Fatalf("reload must keep the workload scaled up (Recreate handles down-then-up), got %v", dep.Spec.Replicas)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Annotations[waasv1alpha1.AnnotationReloadRequestedAt]; ok {
		t.Fatal("the reload annotation must be consumed once applied")
	}
	if got.Spec.Paused {
		t.Fatal("a reload must never touch spec.paused")
	}
	if _, ok := got.Annotations[waasv1alpha1.AnnotationManualStateAt]; ok {
		t.Fatal("a reload must never stamp the manual-state-at annotation (schedule rule B)")
	}
	if cond := findCondition(got, waasv1alpha1.ConditionTemplateDrifted); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected TemplateDrifted=False after reload, got %+v", cond)
	}
}

// TestManualReloadRecreatesBarePod covers the Pod workload kind, whose
// only boundary is recreation — and uses a WORKSPACE OVERRIDE (not a
// template edit) as the drift source, since both feed the fingerprint.
func TestManualReloadRecreatesBarePod(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{Kind: waasv1alpha1.WorkloadPod}
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatal(err)
	}
	hash := pod.Annotations[annotationPodTemplateHash]

	// Runtime reconfiguration (overrides.env) while the pod runs: drift
	// reported, pod untouched.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
		Env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://proxy:3128"}},
	}
	if err := c.Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[annotationPodTemplateHash] != hash {
		t.Fatal("an override edit must not touch the running pod (boundary convergence)")
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if cond := findCondition(got, waasv1alpha1.ConditionTemplateDrifted); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TemplateDrifted=True after an override edit, got %+v", cond)
	}

	// Reload: the pod is deleted (its only boundary); the next reconcile
	// rebuilds it from the up-to-date template, overrides included.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	ws.Annotations = map[string]string{waasv1alpha1.AnnotationReloadRequestedAt: "2026-07-10T09:00:00Z"}
	if err := c.Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err == nil {
		t.Fatal("reload must delete the bare pod (recreation is its boundary)")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.Annotations[waasv1alpha1.AnnotationReloadRequestedAt]; ok {
		t.Fatal("the reload annotation must be consumed once applied")
	}

	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatalf("the next reconcile must recreate the pod: %v", err)
	}
	if pod.Annotations[annotationPodTemplateHash] == hash {
		t.Fatal("the recreated pod must carry the up-to-date template")
	}
	var found bool
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == "HTTP_PROXY" && env.Value == "http://proxy:3128" {
			found = true
		}
	}
	if !found {
		t.Fatal("the recreated pod must carry the env override")
	}
}
