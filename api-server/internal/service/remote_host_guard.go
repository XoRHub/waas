package service

// Remote-workspace host guard (audit finding #7): a remote workspace is
// an OUT-OF-CLUSTER machine, but the user picks the hostname and guacd
// dials it from the platform namespace — so without a check, "remote"
// targets like the kube-apiserver ClusterIP, the cloud IMDS or any
// in-cluster Service name turn the feature into an internal port
// scanner / SSH banner-grabber (guacd only speaks vnc/rdp/ssh, so this
// is not an HTTP SSRF).
//
// THIS IS A GUARDRAIL, NOT A SECURITY BOUNDARY. The api-server
// validates the name here, but guacd is what resolves it when the
// connection is composed — DNS rebinding trivially bypasses this
// check. The structural fix is an egress NetworkPolicy on the platform
// pods, tracked separately. What the guard buys: it closes the naive
// case and turns a mysterious connection timeout into a readable 400.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
)

// resolveTimeout bounds the DNS lookup in Check: it runs inside
// user-facing create/update/connect requests.
const resolveTimeout = 2 * time.Second

// ipResolver is the slice of *net.Resolver the guard uses; tests
// substitute a stub (unit tests cannot rely on real DNS).
type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// HostGuard rejects remote-workspace hostnames that target the cluster
// itself. Built once in main.go and shared by every check.
type HostGuard struct {
	clusterDomain string
	blocked       []*net.IPNet
	resolver      ipResolver
	// apiServerIP is the kube-apiserver ClusterIP read from
	// KUBERNETES_SERVICE_HOST — injected by kubelet into every pod, so
	// free and configuration-less, and the most interesting target of
	// the finding. Nil when the variable is unset (dev mode, unit
	// tests) or not an IP: the guard stays active for everything else
	// rather than failing startup.
	apiServerIP net.IP
}

// ParseBlockedCIDRs validates and parses the configured CIDR list
// (WAAS_REMOTE_BLOCKED_CIDRS — typically the cluster's pod and service
// CIDRs, which nothing can discover reliably from inside a pod). Same
// contract as the operator's envCIDRList (desktop_egress.go): a
// malformed entry is an error and the caller must refuse to start
// rather than silently run with a different posture than what GitOps
// declares. Not imported from there — that helper lives under
// operator/internal, out of this module's reach.
func ParseBlockedCIDRs(cidrs []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", c, err)
		}
		out = append(out, ipNet)
	}
	return out, nil
}

// NewHostGuard builds the guard. An empty clusterDomain falls back to
// cluster.local (the caller normally passes the discovered domain, see
// config.DiscoverClusterDomain).
func NewHostGuard(clusterDomain string, blocked []*net.IPNet) *HostGuard {
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}
	g := &HostGuard{
		clusterDomain: strings.ToLower(strings.Trim(clusterDomain, ".")),
		blocked:       blocked,
		resolver:      net.DefaultResolver,
	}
	if host := os.Getenv("KUBERNETES_SERVICE_HOST"); host != "" {
		if ip := net.ParseIP(host); ip != nil {
			g.apiServerIP = ip
		}
	}
	return g
}

// Check returns an apierror.BadRequest when host targets the cluster.
// A nil receiver disables the guard (unit tests only — main.go always
// wires it).
//
// Two behaviors are DELIBERATE; do not "fix" them:
//
//   - RFC1918 is allowed. The instinct carried over from the desktop
//     egress policy (which does block private ranges) is wrong here: a
//     legitimate remote machine very often sits on a private LAN — an
//     office desktop reached over VPN or peering. What is blocked is
//     the cluster's address space, not private address space.
//
//   - A failed DNS resolution ALLOWS the host (fail-open). Registering
//     a machine that is currently off, or behind DNS the api-server
//     cannot see, must keep working. Consistent with the guard not
//     being a boundary: failing closed would break legitimate use
//     without stopping an attacker, who has rebinding anyway.
func (g *HostGuard) Check(ctx context.Context, host string) error {
	if g == nil {
		return nil
	}
	host = strings.TrimSpace(host)
	// IP literal first: apply the IP filter and return — no DNS
	// resolution, and no pass through the name-shape branch.
	if ip := net.ParseIP(host); ip != nil {
		return g.checkIP(host, ip)
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return denyHost(host)
	}
	// A single-label name resolves through the pod's DNS search path
	// (<name>.<ns>.svc.<domain>, ...) — in-cluster by construction.
	if !strings.Contains(name, ".") {
		return denyHost(host)
	}
	for _, suffix := range []string{".svc", ".svc." + g.clusterDomain, "." + g.clusterDomain} {
		if strings.HasSuffix(name, suffix) {
			return denyHost(host)
		}
	}
	// Resolve and filter every returned address. guacd re-resolves at
	// dial time, so its answer can differ from this one — accepted, see
	// the package comment: this is a guardrail, not a boundary.
	rctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := g.resolver.LookupIPAddr(rctx, host)
	if err != nil {
		slog.Debug("remote host guard: resolution failed, allowing host", "host", host, "error", err)
		return nil
	}
	for _, addr := range addrs {
		if err := g.checkIP(host, addr.IP); err != nil {
			return err
		}
	}
	return nil
}

// checkIP applies the universal, zero-configuration filter plus the
// configured CIDR list. RFC1918 is deliberately absent — see Check.
func (g *HostGuard) checkIP(host string, ip net.IP) error {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return denyHost(host)
	}
	if g.apiServerIP != nil && ip.Equal(g.apiServerIP) {
		return denyHost(host)
	}
	for _, cidr := range g.blocked {
		if cidr.Contains(ip) {
			return denyHost(host)
		}
	}
	return nil
}

func denyHost(host string) error {
	return apierror.BadRequest(fmt.Sprintf(
		"hostname %q points at the cluster's own network; remote workspaces must target machines outside the cluster", host))
}
