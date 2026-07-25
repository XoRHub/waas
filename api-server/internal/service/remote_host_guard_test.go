package service

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
)

// stubResolver maps hostnames to canned answers; an unknown name fails
// like NXDOMAIN. Unit tests cannot rely on real DNS.
type stubResolver struct{ answers map[string][]string }

func (r *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := r.answers[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out, nil
}

// testHostGuard builds a guard the way main.go does, with the
// kube-apiserver ClusterIP env kubelet would inject and a stubbed
// resolver.
func testHostGuard(t *testing.T, blockedCIDRs []string) *HostGuard {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	blocked, err := ParseBlockedCIDRs(blockedCIDRs)
	if err != nil {
		t.Fatalf("parsing blocked CIDRs: %v", err)
	}
	g := NewHostGuard("cluster.local", blocked)
	g.resolver = &stubResolver{answers: map[string][]string{
		// What the pod's DNS search path would answer in-cluster.
		"kubernetes.default": {"10.43.0.1"},
		// A legitimate private-LAN machine (VPN / peering).
		"nas.corp.lan": {"192.168.1.40"},
		// An external name rebound into a configured blocked CIDR.
		"evil.example.com": {"10.42.3.7"},
	}}
	return g
}

func TestHostGuardMatrix(t *testing.T) {
	g := testHostGuard(t, []string{"10.42.0.0/16"})
	cases := []struct {
		host    string
		blocked bool
	}{
		// Blocked: the cluster's own address space.
		{"10.43.0.1", true},          // kube-apiserver ClusterIP (KUBERNETES_SERVICE_HOST)
		{"kubernetes.default", true}, // resolves to the apiserver ClusterIP
		{"169.254.169.254", true},    // cloud IMDS (link-local)
		{"fe80::1", true},            // IPv6 link-local
		{"127.0.0.1", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"localhost", true},
		{"app.localhost", true},
		{"gateway", true}, // single label resolves through the search path
		{"db.svc.cluster.local", true},
		{"db.waas.svc", true},
		{"anything.cluster.local", true},
		{"DB.WAAS.SVC.CLUSTER.LOCAL.", true}, // case and trailing dot do not evade
		{"10.42.9.9", true},                  // configured blocked CIDR (pod/service range)
		{"evil.example.com", true},           // resolves into a blocked CIDR

		// Allowed: RFC1918 is DELIBERATELY not blocked — a legitimate
		// remote machine is very often on a private LAN.
		{"192.168.1.50", false},
		{"10.0.0.5", false}, // private but outside every blocked CIDR
		{"nas.corp.lan", false},
		{"203.0.113.10", false},
		// Fail-open by design: a machine that is off or behind DNS the
		// api-server cannot see must stay registrable.
		{"unresolvable.corp.example", false},
	}
	for _, tc := range cases {
		err := g.Check(context.Background(), tc.host)
		if tc.blocked && !apierror.IsBadRequest(err) {
			t.Errorf("%s: want a 400, got %v", tc.host, err)
		}
		if !tc.blocked && err != nil {
			t.Errorf("%s: want allowed, got %v", tc.host, err)
		}
	}
}

// A custom cluster domain moves the name-suffix rules with it.
func TestHostGuardCustomClusterDomain(t *testing.T) {
	g := NewHostGuard("corp.k8s", nil)
	g.resolver = &stubResolver{}
	if err := g.Check(context.Background(), "db.waas.svc.corp.k8s"); !apierror.IsBadRequest(err) {
		t.Fatalf("custom-domain service name must be blocked, got %v", err)
	}
	// The bare .svc suffix is domain-independent.
	if err := g.Check(context.Background(), "db.waas.svc"); !apierror.IsBadRequest(err) {
		t.Fatalf(".svc suffix must stay blocked regardless of domain, got %v", err)
	}
	// On a corp.k8s cluster, cluster.local names are just external names
	// that do not resolve: fail-open.
	if err := g.Check(context.Background(), "anything.cluster.local"); err != nil {
		t.Fatalf("foreign domain suffix must not be blocked on a corp.k8s cluster, got %v", err)
	}
}

// Outside a cluster (dev mode, unit tests) KUBERNETES_SERVICE_HOST is
// absent: the guard must still work for everything else, never fail.
func TestHostGuardWithoutServiceHostEnv(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	g := NewHostGuard("", nil)
	g.resolver = &stubResolver{}
	if g.apiServerIP != nil {
		t.Fatalf("no env must mean no apiserver IP, got %v", g.apiServerIP)
	}
	if err := g.Check(context.Background(), "169.254.169.254"); !apierror.IsBadRequest(err) {
		t.Fatalf("universal filter must stay active without the env, got %v", err)
	}
}

// A nil guard is disabled — the escape hatch for unit tests that do
// not care about hostnames.
func TestHostGuardNilReceiverDisabled(t *testing.T) {
	var g *HostGuard
	if err := g.Check(context.Background(), "kubernetes.default.svc.cluster.local"); err != nil {
		t.Fatalf("nil guard must allow everything, got %v", err)
	}
}

// Same refuse-to-start contract as the operator's envCIDRList: one
// malformed entry fails the whole parse.
func TestParseBlockedCIDRsRejectsMalformed(t *testing.T) {
	if _, err := ParseBlockedCIDRs([]string{"10.42.0.0/16", "not-a-cidr"}); err == nil {
		t.Fatal("a malformed CIDR must be an error")
	}
	nets, err := ParseBlockedCIDRs([]string{"10.42.0.0/16", "2001:db8::/32"})
	if err != nil || len(nets) != 2 {
		t.Fatalf("valid CIDRs must parse, got %v (%v)", nets, err)
	}
}

// The guard applies on Create and Update, and — the point that matters
// — on Connect: an entry registered before the guard existed must be
// refused when it is actually used, not grandfathered in forever.
func TestRemoteWorkspaceHostGuardEnforced(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}},
		[]waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	actor := Actor{ID: "u1", Username: "u1", Role: "user"}
	f.remote = f.remote.WithHostGuard(testHostGuard(t, nil))

	// Create refuses an in-cluster target.
	if _, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "db", Hostname: "waas-postgres.waas.svc.cluster.local", Port: 22, Protocol: "ssh",
	}); !apierror.IsBadRequest(err) {
		t.Fatalf("in-cluster create must be rejected, got %v", err)
	}

	// A private-LAN machine stays registrable (RFC1918 is allowed).
	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "nas", Hostname: "nas.corp.lan", Port: 22, Protocol: "ssh",
	})
	if err != nil {
		t.Fatalf("private-LAN create must pass: %v", err)
	}

	// Update cannot repoint it inside the cluster.
	if _, err := f.remote.Update(ctx, actor, rw.ID, RemoteWorkspaceInput{
		Name: "nas", Hostname: "10.43.0.1", Port: 22, Protocol: "ssh",
	}); !apierror.IsBadRequest(err) {
		t.Fatalf("in-cluster update must be rejected, got %v", err)
	}

	// Rows stored before the guard (or written around the service) are
	// still refused at connect time.
	now := time.Now().UTC()
	legacy := &model.RemoteWorkspace{
		ID: "legacy-cluster", OwnerID: actor.ID, Name: "legacy-cluster",
		Hostname: "10.43.0.1", SecretName: "waas-remote-legacy-cluster",
		Protocols: []model.RemoteProtocol{{Name: "ssh", Port: 22, Default: true}},
		Protocol:  "ssh", Port: 22,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := f.remotes.Create(ctx, legacy); err != nil {
		t.Fatalf("seeding legacy in-cluster row: %v", err)
	}
	if _, err := f.remote.Connect(ctx, actor, legacy.ID, ConnectInput{}); !apierror.IsBadRequest(err) {
		t.Fatalf("connect to a pre-guard in-cluster entry must be rejected, got %v", err)
	}

	// The legitimate remote still connects.
	if _, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{}); err != nil {
		t.Fatalf("private-LAN connect must pass: %v", err)
	}
}
