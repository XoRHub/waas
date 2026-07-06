package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// remoteFixture wires a RemoteWorkspaceService (and the WorkspaceService
// used by the internal connection resolver) against SQLite + fake kube.
type remoteFixture struct {
	remote    *RemoteWorkspaceService
	workspace *WorkspaceService
	kube      client.Client
	remotes   repository.RemoteWorkspaceRepository
}

func newRemoteFixture(t *testing.T, usersIn []model.User, policies []waasv1alpha1.WorkspacePolicy) *remoteFixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	users := repository.NewSQLUserRepository(db)
	now := time.Now().UTC()
	for i := range usersIn {
		u := usersIn[i]
		if u.Role == "" {
			u.Role = auth.RoleUser
		}
		u.Active, u.CreatedAt, u.UpdatedAt = true, now, now
		if err := users.Create(context.Background(), &u); err != nil {
			t.Fatalf("seeding user %s: %v", u.Username, err)
		}
	}
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}
	for i := range policies {
		p := policies[i]
		p.Namespace = testNS
		if err := kube.Create(context.Background(), &p); err != nil {
			t.Fatalf("seeding policy %s: %v", p.Name, err)
		}
	}
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	sessions := repository.NewSQLSessionRepository(db)
	remotes := repository.NewSQLRemoteWorkspaceRepository(db)
	audit := NewAuditService(repository.NewSQLAuditRepository(db))
	return &remoteFixture{
		remote: NewRemoteWorkspaceService(kube, testNS, users, remotes, sessions,
			audit, signer, "waas-test", time.Minute),
		workspace: NewWorkspaceService(kube, testNS, users, sessions, audit, signer,
			"waas-test", time.Minute).WithRemoteWorkspaces(remotes),
		kube:    kube,
		remotes: remotes,
	}
}

func remotePolicy(enabled bool) waasv1alpha1.WorkspacePolicy {
	return waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec:       waasv1alpha1.WorkspacePolicySpec{RemoteWorkspaces: enabled},
	}
}

func strp(s string) *string { return &s }

// fakeWoL records the MACs it was asked to wake.
type fakeWoL struct {
	woke []string
	err  error
}

func (f *fakeWoL) Wake(_ context.Context, mac string) error {
	if f.err != nil {
		return f.err
	}
	f.woke = append(f.woke, mac)
	return nil
}

func TestRemoteWorkspaceMACAndWake(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	actor := Actor{ID: "u1", Username: "u1", Role: "user"}

	// Invalid MAC is rejected.
	if _, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "bad", Hostname: "h", Port: 22, Protocol: "ssh", MACAddress: "not-a-mac",
	}); !apierror.IsBadRequest(err) {
		t.Fatalf("invalid MAC must be rejected, got %v", err)
	}

	// Valid MAC is normalized to lower colon form.
	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "lab", Hostname: "10.0.0.5", Port: 22, Protocol: "ssh", MACAddress: "AA-BB-CC-DD-EE-FF",
	})
	if err != nil {
		t.Fatalf("create with MAC: %v", err)
	}
	if rw.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("MAC must be normalized, got %q", rw.MACAddress)
	}

	// Wake without a relay configured -> unavailable.
	if err := f.remote.Wake(ctx, actor, rw.ID); err == nil {
		t.Fatal("wake without a relay must fail")
	}

	// With a relay, Wake sends the normalized MAC.
	relay := &fakeWoL{}
	f.remote = f.remote.WithWoL(relay)
	if err := f.remote.Wake(ctx, actor, rw.ID); err != nil {
		t.Fatalf("wake with relay: %v", err)
	}
	if len(relay.woke) != 1 || relay.woke[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("relay must receive the normalized MAC, got %v", relay.woke)
	}

	// A remote without a MAC cannot be woken.
	noMac, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{Name: "nomac", Hostname: "h2", Port: 22, Protocol: "ssh"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.remote.Wake(ctx, actor, noMac.ID); !apierror.IsBadRequest(err) {
		t.Fatalf("wake without MAC must be a bad request, got %v", err)
	}
}

func TestRemoteWorkspacesFailClosed(t *testing.T) {
	ctx := context.Background()
	actor := Actor{ID: "u1", Username: "u1", Role: "user"}
	input := RemoteWorkspaceInput{Name: "lab", Hostname: "10.0.0.5", Port: 22, Protocol: "ssh"}

	// Policy without the flag.
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{remotePolicy(false)})
	if _, err := f.remote.Create(ctx, actor, input); !apierror.IsForbidden(err) {
		t.Fatalf("policy without remoteWorkspaces must deny, got %v", err)
	}
	// No policy at all.
	f = newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}}, nil)
	if _, err := f.remote.List(ctx, actor); !apierror.IsForbidden(err) {
		t.Fatalf("no policy must deny (fail closed), got %v", err)
	}
	// Admins bypass the gate.
	f = newRemoteFixture(t, []model.User{{ID: "a1", Username: "root", Role: auth.RoleAdmin}}, nil)
	if _, err := f.remote.Create(ctx, Actor{ID: "a1", Username: "root", Role: "admin"}, input); err != nil {
		t.Fatalf("admin must bypass the policy gate: %v", err)
	}
}

func TestRemoteWorkspaceLifecycleAndConnection(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}, {ID: "u2", Username: "u2"}},
		[]waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	actor := Actor{ID: "u1", Username: "u1", Role: "user"}

	// Unknown / platform-owned params are rejected by the registry gate.
	if _, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "bad", Hostname: "h", Port: 22, Protocol: "ssh",
		Params: map[string]string{"hostname": "evil"},
	}); !apierror.IsBadRequest(err) {
		t.Fatalf("platform-owned param must be rejected, got %v", err)
	}

	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "lab-server", Hostname: "192.168.1.50", Port: 22, Protocol: "ssh",
		Params: map[string]string{"color-scheme": "green-black"},
		Credentials: &RemoteCredentialsInput{
			Username: strp("root"), Password: strp("hunter2"), PrivateKey: strp("KEYDATA"),
		},
	})
	if err != nil {
		t.Fatalf("creating remote workspace: %v", err)
	}
	if len(rw.CredentialKeys) != 3 {
		t.Fatalf("expected 3 credential keys, got %v", rw.CredentialKeys)
	}

	// The credentials live in a Secret, never in the row or the response.
	secret := &corev1.Secret{}
	if err := f.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "waas-remote-" + rw.ID}, secret); err != nil {
		t.Fatalf("credentials secret must exist: %v", err)
	}
	if string(secret.Data["password"]) != "hunter2" {
		t.Fatalf("secret must hold the password, got %q", secret.Data["password"])
	}

	// Ownership is strict.
	if _, err := f.remote.Get(ctx, Actor{ID: "u2", Username: "u2", Role: "user"}, rw.ID); !apierror.IsNotFound(err) {
		t.Fatalf("foreign remote must be invisible, got %v", err)
	}

	// Credential rotation: nil keeps, empty removes, value replaces.
	rw, err = f.remote.Update(ctx, actor, rw.ID, RemoteWorkspaceInput{
		Name: "lab-server", Hostname: "192.168.1.50", Port: 2222, Protocol: "ssh",
		Credentials: &RemoteCredentialsInput{Password: strp("rotated"), PrivateKey: strp("")},
	})
	if err != nil {
		t.Fatalf("updating remote workspace: %v", err)
	}
	if err := f.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "waas-remote-" + rw.ID}, secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["password"]) != "rotated" || string(secret.Data["username"]) != "root" {
		t.Fatalf("rotation semantics broken: %v", secret.Data)
	}
	if _, still := secret.Data["private-key"]; still {
		t.Fatal("empty string must delete the private-key entry")
	}

	// Connect issues a remote-kind session the internal resolver turns
	// into guacd parameters, credentials included, params merged.
	res, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{Params: map[string]string{"font-size": "16"}})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if res.Protocol != "ssh" || res.ConnectionToken == "" {
		t.Fatalf("unexpected connect result: %+v", res)
	}
	info, err := f.workspace.ConnectionInfo(ctx, res.SessionID)
	if err != nil {
		t.Fatalf("resolving connection info: %v", err)
	}
	if info.Hostname != "192.168.1.50" || info.Port != 2222 || info.Protocol != "ssh" {
		t.Fatalf("target mismatch: %+v", info)
	}
	if info.Username != "root" || info.Password != "rotated" {
		t.Fatalf("credentials must come from the secret: %+v", info)
	}
	if info.Params["font-size"] != "16" {
		t.Fatalf("connect-time params must merge: %+v", info.Params)
	}

	// Delete removes the row AND the credentials secret.
	if err := f.remote.Delete(ctx, actor, rw.ID); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if err := f.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "waas-remote-" + rw.ID}, secret); err == nil {
		t.Fatal("credentials secret must be deleted with the entry")
	}
}

// Multi-protocol remotes: the endpoint list round-trips through the
// repository, legacy single-protocol inputs and rows keep working, the
// connect flow honors the chosen protocol, and the connection resolver
// dials that endpoint's port with its params.
func TestRemoteWorkspaceMultiProtocol(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	actor := Actor{ID: "u1", Username: "u1"}

	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name:     "lab-box",
		Hostname: "203.0.113.10",
		Protocols: []RemoteProtocolInput{
			{Name: "ssh", Port: 22, Default: true, Params: map[string]string{"color-scheme": "green-black"}},
			{Name: "vnc", Port: 5900, Params: map[string]string{"color-depth": "16"}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Legacy mirror fields follow the default endpoint.
	if rw.Protocol != "ssh" || rw.Port != 22 {
		t.Fatalf("legacy fields must mirror the default endpoint, got %s:%d", rw.Protocol, rw.Port)
	}

	// Round-trip through the repository.
	got, err := f.remote.Get(ctx, actor, rw.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Protocols) != 2 || got.ProtocolNamed("vnc") == nil {
		t.Fatalf("protocols must round-trip, got %+v", got.Protocols)
	}

	// Connect on the NON-default endpoint.
	res, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{Protocol: "vnc"})
	if err != nil {
		t.Fatalf("connect vnc: %v", err)
	}
	if res.Protocol != "vnc" {
		t.Fatalf("expected vnc session, got %s", res.Protocol)
	}
	info, err := f.workspace.ConnectionInfo(ctx, res.SessionID)
	if err != nil {
		t.Fatalf("connection info: %v", err)
	}
	if info.Protocol != "vnc" || info.Port != 5900 {
		t.Fatalf("resolver must dial the chosen endpoint, got %s:%d", info.Protocol, info.Port)
	}
	if info.Params["color-depth"] != "16" {
		t.Fatalf("resolver must use the chosen endpoint's params, got %v", info.Params)
	}

	// Unknown protocol is refused.
	if _, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{Protocol: "rdp"}); err == nil {
		t.Fatal("connecting on an undeclared protocol must fail")
	}

	// Duplicate names and multiple defaults are rejected.
	if _, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "dup", Hostname: "h",
		Protocols: []RemoteProtocolInput{{Name: "ssh", Port: 22}, {Name: "ssh", Port: 2222}},
	}); err == nil {
		t.Fatal("duplicate protocol names must be rejected")
	}
}

// Legacy single-protocol input (older clients) still works and yields
// one synthesized endpoint.
func TestRemoteWorkspaceLegacyInputCompat(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	actor := Actor{ID: "u1", Username: "u1"}

	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "old-shape", Hostname: "203.0.113.11", Port: 3389, Protocol: "rdp",
		Params: map[string]string{"color-depth": "24"},
	})
	if err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	protos := rw.EffectiveProtocols()
	if len(protos) != 1 || protos[0].Name != "rdp" || protos[0].Port != 3389 || !protos[0].Default {
		t.Fatalf("legacy input must synthesize one default endpoint, got %+v", protos)
	}
	if protos[0].Params["color-depth"] != "24" {
		t.Fatalf("legacy params must land on the endpoint, got %+v", protos[0].Params)
	}

	// Connect without protocol choice → default endpoint.
	res, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Protocol != "rdp" {
		t.Fatalf("expected default rdp, got %s", res.Protocol)
	}
}
