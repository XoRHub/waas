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
// namespace resolved from the precedence chain (here the built-in
// per-user default) and the trusted username annotation — the governance
// re-check recomputes the default from BOTH, so they must be consistent
// or the deviation counts as a "placement" override.
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
	// Egress toggle off (fixture default): the policy must stay
	// ingress-only, exactly the pre-egress behavior.
	if len(netpol.Spec.PolicyTypes) != 1 || netpol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Fatalf("egress-disabled policy must be ingress-only, got %v", netpol.Spec.PolicyTypes)
	}
	if len(netpol.Spec.Egress) != 0 {
		t.Fatalf("egress-disabled policy must carry no egress rules, got %v", netpol.Spec.Egress)
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

// defaultEgressConfig mirrors what DesktopEgressFromEnv yields without
// any env set: the hardened default posture.
func defaultEgressConfig() DesktopEgressConfig {
	return DesktopEgressConfig{
		Enabled:       true,
		AllowInternet: true,
		BlockedCIDRs:  DefaultBlockedEgressCIDRs(),
	}
}

// assertDesktopEgress checks the full DEFAULT egress shape of the
// namespace policy: DNS to any resolver (UDP+TCP 53) and internet minus
// the IMDS /32 and the RFC1918 ranges, with the ingress side untouched.
func assertDesktopEgress(t *testing.T, netpol *networkingv1.NetworkPolicy) {
	t.Helper()
	if len(netpol.Spec.PolicyTypes) != 2 ||
		netpol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress ||
		netpol.Spec.PolicyTypes[1] != networkingv1.PolicyTypeEgress {
		t.Fatalf("policy must declare Ingress AND Egress, got %v", netpol.Spec.PolicyTypes)
	}
	if len(netpol.Spec.Egress) != 2 {
		t.Fatalf("expected DNS + internet egress rules, got %v", netpol.Spec.Egress)
	}
	// DNS: port 53 to ANY destination (empty To), both protocols. Pinning
	// the resolver would assume a DNS topology we do not control
	// (NodeLocal DNSCache, openshift-dns, node-IP resolvers) and a desktop
	// that cannot resolve is a broken desktop.
	dns := netpol.Spec.Egress[0]
	if len(dns.To) != 0 {
		t.Fatalf("DNS rule must not pin a destination, got %v", dns.To)
	}
	protos := map[corev1.Protocol]bool{}
	for _, p := range dns.Ports {
		if p.Port.IntValue() != 53 {
			t.Fatalf("DNS rule must target port 53, got %v", p.Port)
		}
		protos[*p.Protocol] = true
	}
	if !protos[corev1.ProtocolUDP] || !protos[corev1.ProtocolTCP] {
		t.Fatalf("DNS rule must allow UDP and TCP, got %v", dns.Ports)
	}
	// Internet: 0.0.0.0/0 minus the IMDS /32 and the RFC1918 ranges. The
	// carve-out must be the /32, NOT 169.254.0.0/16 — blocking the whole
	// link-local range would break NodeLocal DNSCache resolution.
	inet := netpol.Spec.Egress[1]
	if len(inet.To) != 1 || inet.To[0].IPBlock == nil || inet.To[0].IPBlock.CIDR != "0.0.0.0/0" {
		t.Fatalf("second egress rule must allow 0.0.0.0/0 with excepts, got %v", inet.To)
	}
	except := map[string]bool{}
	for _, c := range inet.To[0].IPBlock.Except {
		except[c] = true
	}
	for _, want := range []string{"169.254.169.254/32", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if !except[want] {
			t.Fatalf("egress must block %s, got excepts %v", want, inet.To[0].IPBlock.Except)
		}
	}
	if except["169.254.0.0/16"] || len(inet.To[0].IPBlock.Except) != 4 {
		t.Fatalf("egress must block exactly the IMDS /32 + RFC1918 (NodeLocal DNS must stay reachable), got %v",
			inet.To[0].IPBlock.Except)
	}
	// Ingress regression guard: the egress feature must not touch the
	// default-deny ingress (CR namespace + platform namespace admitted).
	var allowed []string
	for _, peer := range netpol.Spec.Ingress[0].From {
		allowed = append(allowed, peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
	if len(allowed) != 2 || allowed[0] != "default" || allowed[1] != "waas-platform" {
		t.Fatalf("ingress peers must be unchanged by the egress feature, got %v", allowed)
	}
}

func TestDesktopEgressPolicyStamped(t *testing.T) {
	ws := placedWorkspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	r.PlatformNamespace = "waas-platform"
	r.DesktopEgress = defaultEgressConfig()

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	assertDesktopEgress(t, netpol)
}

// TestIngressOnlyPolicyHealedToEgress: namespaces provisioned before the
// egress feature carry an ingress-only policy — the every-reconcile sync
// must converge them to ingress+egress once the toggle is on.
func TestIngressOnlyPolicyHealedToEgress(t *testing.T) {
	ws := placedWorkspace()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-alice",
		Labels: map[string]string{labelManagedBy: managerName},
	}}
	r, c := newFixture(t, linuxTemplate(), ws, ns, staleIngressPolicy(map[string]string{labelManagedBy: managerName}))
	r.PlatformNamespace = "waas-platform"
	r.DesktopEgress = defaultEgressConfig()

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	assertDesktopEgress(t, netpol)
}

// TestDesktopEgressCustomConfig: the stamped rules reflect the operator's
// configuration exactly — custom blocked CIDRs land verbatim in the
// except list, and every extra allowed CIDR gets its own dedicated rule
// (which re-opens the range even when the blocked list covers it).
func TestDesktopEgressCustomConfig(t *testing.T) {
	ws := placedWorkspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	r.PlatformNamespace = "waas-platform"
	r.DesktopEgress = DesktopEgressConfig{
		Enabled:       true,
		AllowInternet: true,
		// An operator without NodeLocal DNSCache MAY block the whole
		// link-local range: the except must mirror the config, not the
		// built-in default.
		BlockedCIDRs:      []string{"169.254.0.0/16", "10.0.0.0/8"},
		ExtraAllowedCIDRs: []string{"10.0.5.0/24"},
	}

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	if len(netpol.Spec.Egress) != 3 {
		t.Fatalf("expected DNS + internet + 1 extra rule, got %v", netpol.Spec.Egress)
	}
	inet := netpol.Spec.Egress[1].To[0].IPBlock
	if inet == nil || inet.CIDR != "0.0.0.0/0" ||
		len(inet.Except) != 2 || inet.Except[0] != "169.254.0.0/16" || inet.Except[1] != "10.0.0.0/8" {
		t.Fatalf("except list must mirror the configured blocked CIDRs exactly, got %v", inet)
	}
	extra := netpol.Spec.Egress[2].To[0].IPBlock
	if extra == nil || extra.CIDR != "10.0.5.0/24" || len(extra.Except) != 0 {
		t.Fatalf("each extra allowed CIDR must get a dedicated except-free rule, got %v", extra)
	}
}

// TestDesktopEgressNoInternet: allowInternet=false restricts desktops to
// DNS plus the extra allowed CIDRs — no 0.0.0.0/0 rule anywhere.
func TestDesktopEgressNoInternet(t *testing.T) {
	ws := placedWorkspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	r.PlatformNamespace = "waas-platform"
	cfg := defaultEgressConfig()
	cfg.AllowInternet = false
	cfg.ExtraAllowedCIDRs = []string{"203.0.113.0/24"}
	r.DesktopEgress = cfg

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	if len(netpol.Spec.Egress) != 2 {
		t.Fatalf("expected DNS + extra rules only, got %v", netpol.Spec.Egress)
	}
	for _, rule := range netpol.Spec.Egress {
		for _, to := range rule.To {
			if to.IPBlock != nil && to.IPBlock.CIDR == "0.0.0.0/0" {
				t.Fatalf("allowInternet=false must not emit any 0.0.0.0/0 rule, got %v", netpol.Spec.Egress)
			}
		}
	}
	if ipb := netpol.Spec.Egress[1].To[0].IPBlock; ipb == nil || ipb.CIDR != "203.0.113.0/24" {
		t.Fatalf("extra allowed CIDR must be present without internet, got %v", netpol.Spec.Egress[1].To)
	}
	// The locked-down posture is exactly where a pinned resolver would
	// have killed name resolution: no 0.0.0.0/0 rule left to fall through.
	if dns := netpol.Spec.Egress[0]; len(dns.To) != 0 || len(dns.Ports) != 2 {
		t.Fatalf("DNS must stay open to any resolver without internet, got %v", dns)
	}
}

// TestEgressPolicyHealedBackToIngressOnly: turning the toggle off must
// converge existing ingress+egress policies back to the ingress-only
// shape (a CNI change or an operator taking over egress management).
func TestEgressPolicyHealedBackToIngressOnly(t *testing.T) {
	ws := placedWorkspace()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "waas-alice",
		Labels: map[string]string{labelManagedBy: managerName},
	}}
	withEgress := staleIngressPolicy(map[string]string{labelManagedBy: managerName})
	withEgress.Spec.PolicyTypes = append(withEgress.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
	withEgress.Spec.Egress = desktopEgressRules(defaultEgressConfig())
	r, c := newFixture(t, linuxTemplate(), ws, ns, withEgress)
	r.PlatformNamespace = "waas-platform"
	// The master toggle wins over everything else in the config.
	cfg := defaultEgressConfig()
	cfg.Enabled = false
	r.DesktopEgress = cfg

	reconcile(t, r, ws)

	netpol := &networkingv1.NetworkPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: netpolName}, netpol); err != nil {
		t.Fatal(err)
	}
	if len(netpol.Spec.PolicyTypes) != 1 || netpol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Fatalf("disabling the toggle must heal the policy back to ingress-only, got %v", netpol.Spec.PolicyTypes)
	}
	if len(netpol.Spec.Egress) != 0 {
		t.Fatalf("disabled egress must remove the egress rules, got %v", netpol.Spec.Egress)
	}
	// The ingress side must have been healed too (stale peers converged).
	var allowed []string
	for _, peer := range netpol.Spec.Ingress[0].From {
		allowed = append(allowed, peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
	if len(allowed) != 2 || allowed[0] != "default" || allowed[1] != "waas-platform" {
		t.Fatalf("ingress must stay default-deny with both peers, got %v", allowed)
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

// aggregatePolicy declares aggregate caps so the bootstrap derives a
// namespace quota from them.
func aggregatePolicy(cpu, mem resource.Quantity) *waasv1alpha1.WorkspacePolicy {
	return &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Limits: waasv1alpha1.PolicyLimits{
				Aggregate: &waasv1alpha1.AggregateCaps{CPU: &cpu, Memory: &mem},
			},
		},
	}
}

func xfceCatalogImage() *waasv1alpha1.WorkspaceImage {
	return &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			Image:     "ghcr.io/xorhub/waas/desktop-xfce:latest",
			Enabled:   true,
			Protocols: []waasv1alpha1.Protocol{"vnc"},
		},
	}
}

// TestPlacedNamespaceQuotaFromPolicy: NOMINAL case — the template has no
// placement, the workspace carries the per-user namespace the built-in
// default resolves to. The bootstrapped namespace is personal: ownership
// label AND policy-derived quota.
func TestPlacedNamespaceQuotaFromPolicy(t *testing.T) {
	cpu, mem := resource.MustParse("8"), resource.MustParse("32Gi")
	tpl := linuxTemplate()
	tpl.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws, aggregatePolicy(cpu, mem), xfceCatalogImage())

	reconcile(t, r, ws)

	ns := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "waas-alice"}, ns); err != nil {
		t.Fatalf("expected bootstrapped namespace: %v", err)
	}
	if ns.Labels[labelOwner] != ws.Spec.Owner {
		t.Fatalf("per-user default namespace must carry the ownership label, got %v", ns.Labels)
	}
	quota := &corev1.ResourceQuota{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-alice", Name: "waas-quota"}, quota); err != nil {
		t.Fatalf("expected policy-derived quota: %v", err)
	}
	if quota.Spec.Hard["limits.cpu"] != cpu || quota.Spec.Hard["requests.memory"] != mem {
		t.Fatalf("quota must mirror the aggregate caps, got %v", quota.Spec.Hard)
	}
}

// TestSharedNamespaceGetsNoOwnershipNorQuota: the explicit shared opt-in
// (an admin pins a literal pattern) must bootstrap WITHOUT ownership
// label and WITHOUT auto-quota — several owners share it, and a quota
// derived from one user's caps would throttle the whole group.
func TestSharedNamespaceGetsNoOwnershipNorQuota(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-workspaces"}
	// The policy caps compute: the template must declare its sizing or
	// governance denies before any namespace is bootstrapped.
	tpl.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	ws := placedWorkspace()
	ws.Spec.TargetNamespace = "waas-workspaces"
	r, c := newFixture(t, tpl, ws,
		aggregatePolicy(resource.MustParse("8"), resource.MustParse("32Gi")), xfceCatalogImage())

	reconcile(t, r, ws)

	ns := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "waas-workspaces"}, ns); err != nil {
		t.Fatalf("expected bootstrapped namespace: %v", err)
	}
	if ns.Labels[labelManagedBy] != managerName {
		t.Fatalf("shared namespace must still be operator-managed, got %v", ns.Labels)
	}
	if _, found := ns.Labels[labelOwner]; found {
		t.Fatalf("shared namespace must not carry an ownership label, got %v", ns.Labels)
	}
	err := c.Get(context.Background(), types.NamespacedName{Namespace: "waas-workspaces", Name: "waas-quota"}, &corev1.ResourceQuota{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("shared namespace must not receive an auto-quota, got %v", err)
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
