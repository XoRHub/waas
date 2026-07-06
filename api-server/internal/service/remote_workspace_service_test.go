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
