package controller

// Placement: workloads may live in a dedicated namespace (template
// placement pattern, frozen into spec.targetNamespace at creation) while
// the Workspace CR stays in the platform namespace. This file owns the
// two ends of that split: bootstrapping the target namespace on the way
// up, and the finalizer-driven teardown on the way down (cross-namespace
// objects cannot be garbage-collected through owner references).

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/kubevirt"
	"github.com/xorhub/waas/operator/pkg/metakeys"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// ensureNamespace creates the target namespace and its bootstrap when the
// workspace is placed outside the platform namespace. Create-only: an
// existing namespace is never mutated (admins may have tuned quota,
// network policy or PSA levels — the operator must not fight them).
func (r *WorkspaceReconciler) ensureNamespace(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	name := computeNamespace(ws)
	if name == ws.Namespace {
		return nil
	}
	existing := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching namespace %s: %w", name, err)
	}

	var userLabels, userAnnotations map[string]string
	if p := tpl.Spec.Placement; p != nil {
		userLabels, userAnnotations = p.NamespaceLabels, p.NamespaceAnnotations
	}
	platform := map[string]string{
		labelManagedBy: managerName,
		// Desktop images run non-root but may need baseline-only
		// capabilities (chown at first boot); warn on restricted so
		// hardening candidates surface without breaking sessions.
		"pod-security.kubernetes.io/enforce": "baseline",
		"pod-security.kubernetes.io/warn":    "restricted",
	}
	// The ownership label only belongs on PERSONAL namespaces: shared
	// ones (the built-in "waas-workspace", {os}/{templateName} patterns)
	// host several owners — labeling them with the first creator would
	// wrongly open them to that user's future overrides.
	if isPersonalNamespace(ws) {
		platform[labelOwner] = ws.Spec.Owner
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:        name,
		Labels:      metakeys.MergeAllowed(userLabels, platform),
		Annotations: metakeys.MergeAllowed(userAnnotations, nil),
	}}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %s: %w", name, err)
	}
	r.recordEvent(ws, corev1.EventTypeNormal, "NamespaceCreated",
		fmt.Sprintf("created workload namespace %q (owner %s)", name, ws.Spec.Owner))
	return r.bootstrapNamespace(ctx, ws, name)
}

// bootstrapNamespace installs the accompanying objects of a fresh
// namespace: a ResourceQuota derived from the owner's policy (defense in
// depth — the admission webhook stays the primary enforcement) and a
// default-deny ingress NetworkPolicy that only lets the platform
// namespace in (guacd must reach the desktops; nothing else should).
func (r *WorkspaceReconciler) bootstrapNamespace(ctx context.Context, ws *waasv1alpha1.Workspace, name string) error {
	// The quota derives from the OWNER's aggregate caps: meaningful in a
	// per-user namespace, wrong in a shared one (it would cap the whole
	// group at one user's budget) — shared namespaces get no auto-quota,
	// the admission webhook stays the per-user enforcement everywhere.
	if hard := r.namespaceQuota(ctx, ws); hard != nil && isPersonalNamespace(ws) {
		quota := &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "waas-quota", Namespace: name, Labels: workspaceOwnerLabels(ws)},
			Spec:       corev1.ResourceQuotaSpec{Hard: hard},
		}
		if err := r.Create(ctx, quota); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating resourcequota in %s: %w", name, err)
		}
	}

	// Peers: the CR namespace AND the platform namespace where guacd/wwt
	// actually run (they may differ — chart release ns vs workspaces ns).
	peers := []networkingv1.NetworkPolicyPeer{{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
			"kubernetes.io/metadata.name": ws.Namespace,
		}},
	}}
	if r.PlatformNamespace != "" && r.PlatformNamespace != ws.Namespace {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"kubernetes.io/metadata.name": r.PlatformNamespace,
			}},
		})
	}
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "waas-default-ingress", Namespace: name, Labels: workspaceOwnerLabels(ws)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: peers}},
		},
	}
	if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating networkpolicy in %s: %w", name, err)
	}
	return nil
}

// namespaceQuota derives the ResourceQuota from the owner's resolved
// policy aggregate caps (per-user namespaces make the two speak the same
// unit). No policy or no caps = no quota.
func (r *WorkspaceReconciler) namespaceQuota(ctx context.Context, ws *waasv1alpha1.Workspace) corev1.ResourceList {
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(ws.Namespace)); err != nil {
		return nil
	}
	pol, _, denial := policy.Resolve(policies.Items, policy.IdentityOf(ws))
	if denial != nil || pol == nil || pol.Spec.Limits.Aggregate == nil {
		return nil
	}
	agg := pol.Spec.Limits.Aggregate
	hard := corev1.ResourceList{}
	if agg.CPU != nil {
		hard["requests.cpu"] = *agg.CPU
		hard["limits.cpu"] = *agg.CPU
	}
	if agg.Memory != nil {
		hard["requests.memory"] = *agg.Memory
		hard["limits.memory"] = *agg.Memory
	}
	if agg.Storage != nil {
		hard["requests.storage"] = *agg.Storage
	}
	if len(hard) == 0 {
		return nil
	}
	return hard
}

// workspaceOwnerLabels are the labels for namespace-scoped bootstrap
// objects: ownership without the per-workspace label (the namespace may
// host several workspaces of the same owner).
func workspaceOwnerLabels(ws *waasv1alpha1.Workspace) map[string]string {
	return map[string]string{
		labelManagedBy: managerName,
		labelOwner:     ws.Spec.Owner,
	}
}

// isPersonalNamespace reports whether the workspace's target namespace
// is dedicated to its owner: it matches the identity-derived
// "waas-<user>" prefix (frozen username annotation). Anything else —
// the built-in shared default, {os}/{templateName} patterns — is shared.
func isPersonalNamespace(ws *waasv1alpha1.Workspace) bool {
	username := ws.Annotations[waasv1alpha1.AnnotationUsername]
	if username == "" {
		return false
	}
	userNS := "waas-" + naming.Sanitize(username)
	tns := computeNamespace(ws)
	return tns == userNS || strings.HasPrefix(tns, userNS+"-")
}

// teardownPlacement deletes the cross-namespace compute and service of a
// deleted workspace (the finalizer path). The home PVC is deliberately
// kept — user state survives workspace deletion, exactly like in the
// owner-reference path — then the namespace cleanup policy runs.
func (r *WorkspaceReconciler) teardownPlacement(ctx context.Context, ws *waasv1alpha1.Workspace) error {
	if computeNamespace(ws) == ws.Namespace {
		return nil
	}
	key := computeKey(ws)
	meta := metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}
	objs := []client.Object{
		&appsv1.Deployment{ObjectMeta: meta},
		&appsv1.StatefulSet{ObjectMeta: meta},
		&corev1.Pod{ObjectMeta: meta},
		&corev1.Service{ObjectMeta: meta},
	}
	if r.KubeVirtAvailable {
		vm := &unstructured.Unstructured{}
		vm.SetGroupVersionKind(kubevirt.VirtualMachineGVK)
		vm.SetNamespace(key.Namespace)
		vm.SetName(key.Name)
		objs = append(objs, vm)
	}
	for _, obj := range objs {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting %T %s: %w", obj, key.Name, err)
		}
	}
	return r.cleanupNamespace(ctx, ws)
}

// cleanupNamespace applies the template's namespace cleanup policy after
// a workspace deletion. Retain (the default) never deletes; a vanished
// template also means Retain. DeleteWhenEmpty only fires when the
// operator created the namespace AND nothing waas-managed remains in it —
// home PVCs count, so a namespace holding user state is never deleted.
func (r *WorkspaceReconciler) cleanupNamespace(ctx context.Context, ws *waasv1alpha1.Workspace) error {
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: ws.Spec.TemplateRef}, tpl); err != nil {
		return nil
	}
	if tpl.Spec.CleanupPolicyOrDefault() != waasv1alpha1.CleanupDeleteWhenEmpty {
		return nil
	}
	nsName := computeNamespace(ws)
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: nsName}, ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Labels[labelManagedBy] != managerName {
		return nil
	}
	all := &waasv1alpha1.WorkspaceList{}
	if err := r.List(ctx, all, client.InNamespace(ws.Namespace)); err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	for i := range all.Items {
		sib := &all.Items[i]
		if sib.Name != ws.Name && sib.EffectiveTargetNamespace() == nsName {
			return nil
		}
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(nsName), client.MatchingLabels{labelManagedBy: managerName}); err != nil {
		return fmt.Errorf("listing pvcs in %s: %w", nsName, err)
	}
	if len(pvcs.Items) > 0 {
		return nil
	}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting empty namespace %s: %w", nsName, err)
	}
	return nil
}
