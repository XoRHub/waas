package controller

// Desktop egress hardening of placed namespaces (audit findings #5/#8):
// the shape of the egress side of the operator-owned default network
// policy, its configuration surface (WAAS_DESKTOP_EGRESS_* env vars,
// exposed as operator.desktopEgress.* Helm values) and the env parsing.
// The ingress default-deny is NOT configurable — only egress is.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DesktopEgressConfig shapes the egress side of the default network
// policy stamped on placed namespaces.
type DesktopEgressConfig struct {
	// Enabled is the master toggle: false keeps the policy ingress-only
	// (the escape hatch for CNIs that do not enforce NetworkPolicy
	// egress, or operators managing egress themselves).
	Enabled bool
	// AllowInternet adds a 0.0.0.0/0 rule minus BlockedCIDRs; false
	// restricts desktops to DNS plus ExtraAllowedCIDRs.
	AllowInternet bool
	// BlockedCIDRs are carved out of the internet allowance (ignored
	// when AllowInternet is false — there is no allowance to carve).
	BlockedCIDRs []string
	// ExtraAllowedCIDRs each get a dedicated allow rule: NetworkPolicy
	// rules OR together, so they re-open ranges even when BlockedCIDRs
	// covers them. Applied with or without AllowInternet.
	ExtraAllowedCIDRs []string
}

// DefaultBlockedEgressCIDRs is the default internet carve-out: the cloud
// IMDS /32 (node credentials on EKS/GKE/AKS) and the RFC1918 ranges
// (cluster pod/service CIDRs, node IPs and the platform namespace —
// guacd/wwt connect INTO the desktops, which is ingress; a desktop never
// needs egress to the platform). Only the IMDS /32 of the link-local
// range, not the whole 169.254.0.0/16: the rest of it is node-local
// plumbing (NodeLocal DNSCache, CNI gateways) that blocking buys nothing
// against — a pod reaching it is already on that node.
//
// Scope: a VANILLA Kubernetes, whose Service and Pod networks are RFC1918
// (kubeadm: 10.96.0.0/12 and 10.244.0.0/16). A provider that places the
// Service CIDR outside RFC1918 — GKE defaults to 34.118.224.0/20 — leaves
// the kube-apiserver reachable from desktops until the admin appends that
// range to operator.desktopEgress.blockedCIDRs. Shipping a per-provider
// table instead would rot silently, which is worse than one documented
// gap: see the blockedCIDRs comment in values.yaml.
func DefaultBlockedEgressCIDRs() []string {
	return []string{
		"169.254.169.254/32", // cloud IMDS (AWS/GCP/Azure all use .254)
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
}

// DesktopEgressFromEnv reads the WAAS_DESKTOP_EGRESS_* variables.
// Fail-closed: a MISSING variable falls back to the hardened default
// (enabled, internet minus DefaultBlockedEgressCIDRs) — only explicit
// values loosen the posture (an explicitly empty blocked list is
// honored: Helm renders `blockedCIDRs: []` as a present-but-empty env).
// Any unparseable bool or CIDR is an error: the caller must refuse to
// start rather than silently run with a different egress posture than
// what GitOps declares.
func DesktopEgressFromEnv() (DesktopEgressConfig, error) {
	cfg := DesktopEgressConfig{}
	var err error
	if cfg.Enabled, err = envBool("WAAS_DESKTOP_EGRESS_ENABLED", true); err != nil {
		return cfg, err
	}
	if cfg.AllowInternet, err = envBool("WAAS_DESKTOP_EGRESS_ALLOW_INTERNET", true); err != nil {
		return cfg, err
	}
	if cfg.BlockedCIDRs, err = envCIDRList("WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", DefaultBlockedEgressCIDRs()); err != nil {
		return cfg, err
	}
	if err = validateExceptCIDRs("WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", cfg.BlockedCIDRs); err != nil {
		return cfg, err
	}
	if cfg.ExtraAllowedCIDRs, err = envCIDRList("WAAS_DESKTOP_EGRESS_EXTRA_ALLOWED_CIDRS", nil); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// envBool parses a boolean env var; missing or empty = the default.
func envBool(name string, def bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("%s: invalid boolean %q: %w", name, v, err)
	}
	return parsed, nil
}

// envCIDRList parses a comma-separated CIDR list. Missing = the default;
// present (even empty) = explicit, every entry validated.
func envCIDRList(name string, def []string) ([]string, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def, nil
	}
	var out []string
	for _, c := range strings.Split(v, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(c); err != nil {
			return nil, fmt.Errorf("%s: invalid CIDR %q: %w", name, c, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// validateExceptCIDRs rejects entries the API server would refuse inside
// the IPBlock.Except of the 0.0.0.0/0 internet rule. ValidateIPBlock
// wants every except to be a STRICT subset of the block, i.e. IPv4 with
// a prefix length above 0 — but net.ParseCIDR happily accepts "::/0",
// "fd00::/8" or "0.0.0.0/0". Such an entry passes startup and then makes
// EVERY NetworkPolicy write fail on EVERY reconcile of EVERY workspace,
// with the cause visible only in the operator logs. Fail here instead.
func validateExceptCIDRs(name string, cidrs []string) error {
	for _, c := range cidrs {
		ip, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("%s: invalid CIDR %q: %w", name, c, err)
		}
		if ip.To4() == nil {
			return fmt.Errorf("%s: %q must be IPv4: it is carved out of the 0.0.0.0/0 internet rule, "+
				"which Kubernetes requires every exception to be a strict subset of", name, c)
		}
		if ones, _ := ipNet.Mask.Size(); ones == 0 {
			return fmt.Errorf("%s: %q is not a strict subset of 0.0.0.0/0: use a prefix length above 0 "+
				"(set operator.desktopEgress.allowInternet=false to deny the internet wholesale)", name, c)
		}
	}
	return nil
}

// desktopEgressRules is the default-deny egress of a placed namespace.
// Everything not explicitly allowed here — kube-API, kubelet, platform
// services, other namespaces — is denied.
func desktopEgressRules(cfg DesktopEgressConfig) []networkingv1.NetworkPolicyEgressRule {
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	dns := intstr.FromInt32(53)
	// DNS is unconditional and destination-agnostic: an empty To means
	// "any destination", narrowed to port 53. A desktop that cannot
	// resolve is a broken desktop, and every way of naming the resolver
	// assumes a topology we do not control — NodeLocal DNSCache answers on
	// a link-local IP no selector can match, OpenShift runs CoreDNS in
	// openshift-dns, some clusters point resolv.conf at a node IP. Cost of
	// the shortcut: with AllowInternet (the default) it grants strictly
	// nothing the internet rule does not already grant; without it, port
	// 53 stays open outbound, i.e. a DNS-tunneling channel remains.
	rules := []networkingv1.NetworkPolicyEgressRule{{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &dns},
			{Protocol: &tcp, Port: &dns},
		},
	}}
	if cfg.AllowInternet {
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{
				IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: cfg.BlockedCIDRs},
			}},
		})
	}
	// Egress rules OR together: a dedicated allow per extra CIDR re-opens
	// the range even when BlockedCIDRs covers it.
	for _, c := range cfg.ExtraAllowedCIDRs {
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: c}}},
		})
	}
	return rules
}
