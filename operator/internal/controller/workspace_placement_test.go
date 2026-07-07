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
	ctrl "sigs.k8s.io/controller-runtime"

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
	r.PlatformNamespace = "waas-platform" // where guacd/wwt run
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
	// guacd/wwt run in the platform (release) namespace, which may differ
	// from the CR namespace: BOTH must be allowed in or placed desktops
	// become unreachable through the proxy.
	var allowed []string
	for _, peer := range netpol.Spec.Ingress[0].From {
		allowed = append(allowed, peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
	if len(allowed) != 2 || allowed[0] != "default" || allowed[1] != "waas-platform" {
		t.Fatalf("netpol must admit the CR namespace and the platform namespace, got %v", allowed)
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

// staleIngressPolicy reproduces what an operator deployed WITHOUT its
// platform namespace stamped into placed namespaces: only the CR
// namespace admitted, guacd (platform namespace) rejected — the exact
// shape of the VNC/RDP "connection closed" regression.
func staleIngressPolicy(labels map[string]string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "waas-alice", Name: netpolName, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": "default",
				}},
			}}}},
		},
	}
}

func TestStaleIngressPolicyIsHealed(t *testing.T) {
	ws := placedWorkspace()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-alice",
		Labels: map[string]string{labelManagedBy: managerName},
	}}
	r, c := newFixture(t, linuxTemplate(), ws, ns, staleIngressPolicy(map[string]string{labelManagedBy: managerName}))
	r.PlatformNamespace = "waas-platform"

	reconcile(t, r, ws)

	// The bootstrap is create-only for admin tunables (quota, PSA), but
	// the operator-owned ingress policy must converge even when the
	// namespace pre-exists: create-only left guacd locked out forever.
	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	var allowed []string
	for _, peer := range netpol.Spec.Ingress[0].From {
		allowed = append(allowed, peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
	if len(allowed) != 2 || allowed[0] != "default" || allowed[1] != "waas-platform" {
		t.Fatalf("stale ingress policy must be healed to admit the platform namespace, got %v", allowed)
	}
}

func TestAdminOwnedIngressPolicyIsLeftAlone(t *testing.T) {
	ws := placedWorkspace()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-alice",
		Labels: map[string]string{labelManagedBy: managerName},
	}}
	// No managed-by label: an admin replaced the policy on purpose.
	r, c := newFixture(t, linuxTemplate(), ws, ns, staleIngressPolicy(nil))
	r.PlatformNamespace = "waas-platform"

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	if len(netpol.Spec.Ingress[0].From) != 1 {
		t.Fatalf("an admin-owned ingress policy must never be rewritten, got %v", netpol.Spec.Ingress[0].From)
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

// reconcileNS drives the janitor over one namespace.
func reconcileNS(t *testing.T, j *NamespaceJanitor, name string) {
	t.Helper()
	if _, err := j.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
		t.Fatalf("janitor reconcile: %v", err)
	}
}

// TestPlacedNamespaceDeleteWhenEmpty exercises the REAL deletion order: the
// home PVC stays Terminating under kubernetes.io/pvc-protection while the
// finalizer runs (simulated with a test finalizer), so the namespace must
// survive the workspace deletion and only be reclaimed by the janitor once
// the PVC is actually gone. The previous version of this test deleted the
// PVC synchronously before the CR, which masked exactly that bug.
func TestPlacedNamespaceDeleteWhenEmpty(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{
		Namespace: "waas-{user}",
		Cleanup:   waasv1alpha1.CleanupDeleteWhenEmpty,
	}
	ws := placedWorkspace()
	// The user chose to delete the home volume with the workspace.
	ws.Annotations[waasv1alpha1.AnnotationDeleteHome] = "true"
	r, c := newFixture(t, tpl, ws)
	j := &NamespaceJanitor{Client: c}
	ctx := context.Background()

	reconcile(t, r, ws)

	// The namespace froze the cleanup policy at creation.
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, ns); err != nil {
		t.Fatal(err)
	}
	if ns.Labels[waasv1alpha1.LabelCleanup] != string(waasv1alpha1.CleanupDeleteWhenEmpty) {
		t.Fatalf("namespace must freeze the cleanup policy label, got %v", ns.Labels)
	}

	// Pin the PVC in Terminating, like pvc-protection does while the
	// desktop pod drains.
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station-home"}, pvc); err != nil {
		t.Fatal(err)
	}
	pvc.Finalizers = append(pvc.Finalizers, "kubernetes.io/pvc-protection")
	if err := c.Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws) // finalizer path: deletes content, CR goes away

	// PVC still Terminating: the janitor must NOT reclaim the namespace yet.
	reconcileNS(t, j, "waas-alice")
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace must survive while the PVC is still terminating: %v", err)
	}

	// pvc-protection resolves (pod gone): the PVC disappears for real,
	// which in production re-triggers the janitor through its watch.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "waas-alice", Name: "cad-station-home"}, pvc); err != nil {
		t.Fatal(err)
	}
	pvc.Finalizers = nil
	if err := c.Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	reconcileNS(t, j, "waas-alice")

	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("empty DeleteWhenEmpty namespace must be reclaimed once the PVC is gone, got %v", err)
	}
}

// TestDeleteWhenEmptyKeepsNamespaceHoldingUserState: a retained home
// volume holds the namespace open; deleting that volume later (volumes
// API) must finally reclaim it — without any workspace CR involved.
func TestDeleteWhenEmptyKeepsNamespaceHoldingUserState(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{
		Namespace: "waas-{user}",
		Cleanup:   waasv1alpha1.CleanupDeleteWhenEmpty,
	}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws)
	j := &NamespaceJanitor{Client: c}
	ctx := context.Background()

	reconcile(t, r, ws)
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	// The retained home PVC is user state: the namespace must NOT be
	// deleted even under DeleteWhenEmpty.
	reconcileNS(t, j, "waas-alice")
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace holding a retained volume must be kept: %v", err)
	}

	// The user deletes the retained volume weeks later: the janitor is
	// re-triggered by the PVC deletion event and reclaims the namespace.
	if err := c.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "waas-alice", Name: "cad-station-home"}}); err != nil {
		t.Fatal(err)
	}
	reconcileNS(t, j, "waas-alice")
	if err := c.Get(ctx, types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("namespace must be reclaimed once the retained volume is deleted, got %v", err)
	}
}

// TestJanitorNeverGuessesPolicy: managed namespaces without the frozen
// cleanup label (pre-migration) and unmanaged namespaces are never
// deleted, however empty they are.
func TestJanitorNeverGuessesPolicy(t *testing.T) {
	legacy := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-legacy",
		Labels: map[string]string{labelManagedBy: managerName},
	}}
	foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "team-prod",
		Labels: map[string]string{
			waasv1alpha1.LabelCleanup: string(waasv1alpha1.CleanupDeleteWhenEmpty),
		},
	}}
	retain := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "waas-retain",
		Labels: map[string]string{
			labelManagedBy:            managerName,
			waasv1alpha1.LabelCleanup: string(waasv1alpha1.CleanupRetain),
		},
	}}
	_, c := newFixture(t, legacy, foreign, retain)
	j := &NamespaceJanitor{Client: c}
	for _, name := range []string{"waas-legacy", "team-prod", "waas-retain"} {
		reconcileNS(t, j, name)
		if err := c.Get(context.Background(), types.NamespacedName{Name: name}, &corev1.Namespace{}); err != nil {
			t.Fatalf("namespace %s must never be deleted by the janitor: %v", name, err)
		}
	}
}

// TestJanitorKeepsNamespaceOfPendingWorkspace: a workspace targeting the
// namespace holds it open even before any compute exists (Pending /
// governance-denied workspaces must not lose their namespace under them).
func TestJanitorKeepsNamespaceOfPendingWorkspace(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "waas-alice",
		Labels: map[string]string{
			labelManagedBy:            managerName,
			waasv1alpha1.LabelCleanup: string(waasv1alpha1.CleanupDeleteWhenEmpty),
		},
	}}
	ws := placedWorkspace()
	_, c := newFixture(t, ns, ws)
	j := &NamespaceJanitor{Client: c}

	reconcileNS(t, j, "waas-alice")
	if err := c.Get(context.Background(), types.NamespacedName{Name: "waas-alice"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("namespace targeted by a live workspace must be kept: %v", err)
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
