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

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
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
		// The namespace itself is admin territory once it exists, but the
		// operator's OWN ingress policy is desired-state: the set of
		// admitted namespaces depends on deployment config (platform
		// namespace), and a policy stamped by a misconfigured operator
		// would otherwise lock guacd out of the namespace forever.
		if err := r.ensureCleanupLabel(ctx, tpl, existing); err != nil {
			return err
		}
		return r.ensureNamespacePolicy(ctx, ws, name)
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
		// The cleanup policy is FROZEN here: the janitor reads this label,
		// never the template, so a template deleted before its workspaces
		// cannot silently turn DeleteWhenEmpty into Retain.
		waasv1alpha1.LabelCleanup: string(tpl.Spec.CleanupPolicyOrDefault()),
		// baseline enforced, restricted warned. NOT because the images
		// need baseline — measured 2026-07-25, the catalog images run
		// fine under restricted (see docs/placement.md) — but because
		// the enforce level is the cluster admin's call, not ours: a
		// desktop pod spec carries no container securityContext of its
		// own, deliberately, so that a cluster-wide mutation policy
		// (Kyverno, MutatingAdmissionPolicy, "add if absent") stays the
		// thing that fills it. Pre-filling it here would silently take
		// desktop pods out of that policy's reach. warn=restricted is
		// the canary that lets an admin see what raising enforce would
		// cost. Create-only (see above): relabel and the operator will
		// not fight you.
		"pod-security.kubernetes.io/enforce": "baseline",
		"pod-security.kubernetes.io/warn":    "restricted",
	}
	// The ownership label only belongs on PERSONAL namespaces — the
	// nominal case under the built-in "waas-{user}" default. Shared ones
	// (admin-chosen literals like "waas-workspaces", {os}/{templateName}
	// patterns) host several owners — labeling them with the first
	// creator would wrongly open them to that user's future overrides.
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
	// per-user namespace — the nominal case now that the built-in default
	// isolates per user — wrong in an opt-in shared one (it would cap the
	// whole group at one user's budget). Shared namespaces get no
	// auto-quota; the admission webhook stays the per-user enforcement
	// everywhere.
	if hard := r.namespaceQuota(ctx, ws); hard != nil && isPersonalNamespace(ws) {
		quota := &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "waas-quota", Namespace: name, Labels: workspaceOwnerLabels(ws)},
			Spec:       corev1.ResourceQuotaSpec{Hard: hard},
		}
		if err := r.Create(ctx, quota); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating resourcequota in %s: %w", name, err)
		}
	}

	return r.ensureNamespacePolicy(ctx, ws, name)
}

// ensureCleanupLabel back-fills the frozen cleanup-policy label on
// operator-created namespaces that predate it (migration path: without
// it the janitor would treat them as Retain forever). Only ever fills a
// MISSING label — an existing value is frozen, whatever the template
// says today — and never touches namespaces the operator does not own.
func (r *WorkspaceReconciler) ensureCleanupLabel(ctx context.Context, tpl *waasv1alpha1.WorkspaceTemplate, ns *corev1.Namespace) error {
	if ns.Labels[labelManagedBy] != managerName || ns.Labels[waasv1alpha1.LabelCleanup] != "" {
		return nil
	}
	ns.Labels[waasv1alpha1.LabelCleanup] = string(tpl.Spec.CleanupPolicyOrDefault())
	if err := r.Update(ctx, ns); err != nil {
		return fmt.Errorf("stamping cleanup policy on namespace %s: %w", ns.Name, err)
	}
	return nil
}

// netpolName is the operator-owned default network policy of a placed
// namespace. The name is a slight misnomer since the policy grew an
// egress side, but it MUST stay "waas-default-ingress": the operator only
// manages the object carrying this name, and renaming it would orphan the
// policy in every already-deployed cluster.
const netpolName = "waas-default-ingress"

// ensureNamespacePolicy creates or heals the default network policy of a
// placed namespace: default-deny ingress where only the CR namespace and
// the platform namespace (guacd/wwt) may reach the desktops, plus — when
// DesktopEgress.Enabled — a default-deny egress (see desktop_egress.go:
// DNS + configurable internet/extra allowances, IMDS and internal ranges
// blocked by default). Unlike the rest of the bootstrap
// this is synced on EVERY reconcile — the create-only path never
// revisited a policy written by an operator that did not know its
// platform namespace, leaving guacd rejected until someone deleted the
// namespace by hand. A policy that lost the managed-by label is an admin
// takeover and is left alone.
func (r *WorkspaceReconciler) ensureNamespacePolicy(ctx context.Context, ws *waasv1alpha1.Workspace, name string) error {
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
	desired := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: peers}},
	}
	// The egress side is toggleable (CNIs that do not enforce egress, or
	// operators managing their own); the ingress default-deny never is.
	// Since this syncs on every reconcile, flipping the toggle converges
	// existing namespaces either way on the next pass.
	if r.DesktopEgress.Enabled {
		desired.PolicyTypes = append(desired.PolicyTypes, networkingv1.PolicyTypeEgress)
		desired.Egress = desktopEgressRules(r.DesktopEgress)
	}

	existing := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{Namespace: name, Name: netpolName}, existing)
	if apierrors.IsNotFound(err) {
		netpol := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: netpolName, Namespace: name, Labels: workspaceOwnerLabels(ws)},
			Spec:       desired,
		}
		if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating networkpolicy in %s: %w", name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching networkpolicy in %s: %w", name, err)
	}
	if existing.Labels[labelManagedBy] != managerName {
		return nil
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired) {
		return nil
	}
	existing.Spec = desired
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("healing networkpolicy in %s: %w", name, err)
	}
	admitted := make([]string, 0, len(peers))
	for _, p := range peers {
		admitted = append(admitted, p.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
	r.recordEvent(ws, corev1.EventTypeNormal, "NamespacePolicyHealed",
		fmt.Sprintf("synced default network policy of namespace %q (admitted: %s)", name, strings.Join(admitted, ", ")))
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
// is dedicated to its owner: it matches the identity-derived personal
// namespace (frozen username annotation) or a suffixed variant of it —
// the nominal case, since the built-in "waas-{user}" default resolves
// exactly there. Anything else — an admin-chosen shared literal
// ("waas-workspaces"), {os}/{templateName} patterns — is shared.
// naming.PersonalNamespace is the shared resolution: computing the name
// here would drift from what the api-server actually froze into the spec
// for long usernames, and this workspace's namespace would silently lose
// its ownership label and its quota.
func isPersonalNamespace(ws *waasv1alpha1.Workspace) bool {
	username := ws.Annotations[waasv1alpha1.AnnotationUsername]
	if username == "" {
		return false
	}
	userNS := naming.PersonalNamespace(username)
	if userNS == "" {
		return false
	}
	tns := computeNamespace(ws)
	return tns == userNS || strings.HasPrefix(tns, userNS+"-")
}

// teardownPlacement deletes the cross-namespace compute and service of a
// deleted workspace (the finalizer path). The type list derives from the
// single managed-types inventory; the home PVC is skipped on purpose —
// finalizeHomeVolume applied the user's retention choice before this
// runs. The namespace itself is NOT handled here: content deletion is
// asynchronous (pvc-protection, pod grace periods), so reclaiming empty
// DeleteWhenEmpty namespaces belongs to the namespace janitor, which the
// deletion events re-trigger once the objects are actually gone.
func (r *WorkspaceReconciler) teardownPlacement(ctx context.Context, ws *waasv1alpha1.Workspace) error {
	if computeNamespace(ws) == ws.Namespace {
		return nil
	}
	key := computeKey(ws)
	for _, gvk := range waasv1alpha1.WorkspaceContentGVKs() {
		if gvk.Kind == "PersistentVolumeClaim" {
			continue
		}
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetNamespace(key.Namespace)
		obj.SetName(key.Name)
		// A VM delete on a cluster without KubeVirt is a no-op, not an
		// error — and deliberately NOT gated on the startup detection: an
		// operator restarted during a KubeVirt outage must not leak VMs.
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) &&
			!meta.IsNoMatchError(err) && !runtime.IsNotRegisteredError(err) {
			return fmt.Errorf("deleting %s %s: %w", gvk.Kind, key.Name, err)
		}
	}
	// The generated-ssh pod copy is the one content object NOT named
	// after the workload (suffixed, since ssh coexists with vnc/rdp and
	// cannot share the bare name) — the loop above misses it.
	sshCopy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: sshPodSecretName(ws)}}
	if err := r.Delete(ctx, sshCopy); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Secret %s: %w", sshCopy.Name, err)
	}
	return nil
}
