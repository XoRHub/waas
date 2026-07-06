package controller

// Placement tests: workloads land in the frozen spec.targetNamespace with
// the operator-created namespace bootstrap (ownership + Pod Security
// labels, policy-derived ResourceQuota, default-deny ingress), are torn
// down through the finalizer at deletion (owner references cannot cross
// namespaces), and the namespace cleanup policy is honored — Retain by
// default, DeleteWhenEmpty only when no waas object (home PVC included)
// remains.

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// placedWorkspace mirrors what the api-server produces: the target
// namespace resolved from the template pattern and the trusted username
// annotation — the governance re-check recomputes the default from BOTH,
// so they must be consistent or the deviation counts as a "placement"
// override.
func placedWorkspace() *waasv1alpha1.Workspace {
	ws := workspace()
	ws.Annotations = map[string]string{waasv1alpha1.AnnotationUsername: "alice"}
	ws.Spec.TargetNamespace = "waas-alice"
	ws.Spec.WorkloadName = "cad-station"
	return ws
}

func TestPlacedWorkspaceProvisionsInTargetNamespace(t *testing.T) {
	ws := placedWorkspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// The namespace was bootstrapped with ownership + Pod Security labels.
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, ns); err != nil {
		t.Fatalf("expected bootstrapped namespace: %v", err)
	}
	if ns.Labels[labelOwner] != ws.Spec.Owner || ns.Labels[labelManagedBy] != managerName {
		t.Fatalf("namespace must carry ownership labels, got %v", ns.Labels)
	}
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Fatalf("namespace must carry PSA labels, got %v", ns.Labels)
	}
	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "waas-default-ingress"}, netpol); err != nil {
		t.Fatalf("expected default ingress networkpolicy: %v", err)
	}

	// Workload, service and PVC are named after the workspace and live in
	// the target namespace, without cross-namespace owner references.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station"}, dep); err != nil {
		t.Fatalf("expected deployment in target namespace: %v", err)
	}
	if len(dep.OwnerReferences) != 0 {
		t.Fatalf("cross-namespace deployment must not carry an owner reference")
	}
	if dep.Labels[labelWorkspaceNS] != "default" || dep.Labels[labelWorkspace] != "marc" {
		t.Fatalf("deployment must map back to its CR through labels, got %v", dep.Labels)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station-home"}, pvc); err != nil {
		t.Fatalf("expected home PVC in target namespace: %v", err)
	}

	// The CR gained the teardown finalizer and advertises the placed DNS name.
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range got.Finalizers {
		found = found || f == finalizerTeardown
	}
	if !found {
		t.Fatalf("placed workspace must carry the teardown finalizer, got %v", got.Finalizers)
	}
	if got.Status.Address != "cad-station.waas-alice.svc.cluster.local" {
		t.Fatalf("status address must point at the target namespace, got %q", got.Status.Address)
	}
}

func TestPlacedNamespaceQuotaFromPolicy(t *testing.T) {
	cpu, mem := resource.MustParse("8"), resource.MustParse("32Gi")
	pol := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Limits: waasv1alpha1.PolicyLimits{
				Aggregate: &waasv1alpha1.AggregateCaps{CPU: &cpu, Memory: &mem},
			},
		},
	}
	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			Image:     "ghcr.io/xorhub/waas/desktop-xfce:latest",
			Enabled:   true,
			Protocols: []waasv1alpha1.Protocol{"vnc"},
		},
	}
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-{user}"}
	tpl.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws, pol, img)

	reconcile(t, r, ws)

	quota := &corev1.ResourceQuota{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: "waas-quota"}, quota); err != nil {
		t.Fatalf("expected policy-derived quota: %v", err)
	}
	if quota.Spec.Hard["limits.cpu"] != cpu || quota.Spec.Hard["requests.memory"] != mem {
		t.Fatalf("quota must mirror the aggregate caps, got %v", quota.Spec.Hard)
	}
}

func TestPlacedWorkspaceTeardownKeepsPVCAndNamespace(t *testing.T) {
	ws := placedWorkspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws) // finalizer path

	// CR fully gone (finalizer removed), compute and service deleted.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, &waasv1alpha1.Workspace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("workspace must be gone after teardown, got %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station"}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("deployment must be torn down, got %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station"}, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Fatalf("service must be torn down, got %v", err)
	}
	// User state and (Retain default) the namespace survive.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station-home"}, &corev1.PersistentVolumeClaim{}); err != nil {
		t.Fatalf("home PVC must survive deletion: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("Retain policy must keep the namespace: %v", err)
	}
}

func TestPlacedNamespaceDeleteWhenEmpty(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{
		Namespace: "waas-{user}",
		Cleanup:   waasv1alpha1.CleanupDeleteWhenEmpty,
	}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// Simulate the TTL path having reclaimed the home volume, then delete.
	if err := c.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "waas-alice", Name: "cad-station-home"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("empty operator-created namespace must be deleted under DeleteWhenEmpty, got %v", err)
	}
}

func TestDeleteWhenEmptyKeepsNamespaceHoldingUserState(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{
		Namespace: "waas-{user}",
		Cleanup:   waasv1alpha1.CleanupDeleteWhenEmpty,
	}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	// The home PVC is still there: the namespace must NOT be deleted even
	// under DeleteWhenEmpty.
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace holding a home PVC must be kept: %v", err)
	}
}

func TestCustomWorkloadMetadataPlatformWins(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{
		Labels:      map[string]string{"team": "cad"},
		Annotations: map[string]string{"example.com/note": "template"},
	}
	ws := workspace()
	ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
		Labels: map[string]string{
			"cost-center":               "42",
			waasv1alpha1.LabelWorkspace: "spoofed", // must never override the selector label
		},
	}
	r, c := newFixture(t, tpl, ws)

	reconcile(t, r, ws)

	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []map[string]string{dep.Labels, dep.Spec.Template.Labels} {
		if obj["team"] != "cad" || obj["cost-center"] != "42" {
			t.Fatalf("custom labels must reach workload and pod template, got %v", obj)
		}
		if obj[labelWorkspace] != "marc" {
			t.Fatalf("platform label must win over a spoofed override, got %v", obj)
		}
	}
	if dep.Annotations["example.com/note"] != "template" {
		t.Fatalf("template annotations must reach the workload, got %v", dep.Annotations)
	}
}
