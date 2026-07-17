package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// newConnectionFixture wires the minimal WorkspaceService surface that
// ConnectionInfo needs: the kube client and the session repository.
func newConnectionFixture(t *testing.T) (*WorkspaceService, client.WithWatch, repository.SessionRepository) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "conn.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}
	// Sessions have a foreign key on users; seed the session's owner.
	users := repository.NewSQLUserRepository(db)
	now := time.Now().UTC()
	if err := users.Create(context.Background(), &model.User{
		ID: "u1", Username: "u1", Role: "user", Active: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	sessions := repository.NewSQLSessionRepository(db)
	return &WorkspaceService{kube: kube, namespace: testNS, sessions: sessions}, kube, sessions
}

// seedDesktopWorkspace creates a running vnc+rdp workspace bound to the
// given template and an open session on the requested protocol.
func seedDesktopWorkspace(t *testing.T, kube client.WithWatch, sessions repository.SessionRepository,
	tpl *waasv1alpha1.WorkspaceTemplate, protocol string) *waasv1alpha1.Workspace {
	t.Helper()
	ctx := context.Background()
	if err := kube.Create(ctx, tpl); err != nil {
		t.Fatalf("seeding template: %v", err)
	}
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "marc", Namespace: testNS},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: tpl.Name, Owner: "u1"},
	}
	if err := kube.Create(ctx, ws); err != nil {
		t.Fatalf("seeding workspace: %v", err)
	}
	ws.Status = waasv1alpha1.WorkspaceStatus{
		Phase:    waasv1alpha1.PhaseRunning,
		Address:  "10.0.0.10",
		Protocol: "vnc",
		Port:     5901,
		Protocols: []waasv1alpha1.WorkspaceProtocolStatus{
			{Name: "vnc", Port: 5901, Default: true},
			{Name: "rdp", Port: 3389},
		},
	}
	if err := kube.Status().Update(ctx, ws); err != nil {
		t.Fatalf("setting workspace status: %v", err)
	}
	if err := sessions.Create(ctx, &model.Session{
		ID: "s-" + protocol, UserID: "u1", WorkspaceID: string(ws.UID),
		WorkspaceName: ws.Name, Protocol: protocol, StartedAt: time.Now().UTC(),
		Kind: model.SessionKindWorkspace,
	}); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	return ws
}

func desktopServiceTemplate() *waasv1alpha1.WorkspaceTemplate {
	return &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "XFCE",
			OS:          waasv1alpha1.OSLinux,
			Image:       "reg/xfce:1",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "vnc", Port: 5901, Default: true},
				{Name: "rdp", Port: 3389},
			},
		},
	}
}

// The generated-password fallback: with no explicit source, both vnc and
// rdp resolve the operator's waas-desktop-<name> Secret and default the
// username to the waas-images system account.
func TestConnectionInfoGeneratedDesktopPassword(t *testing.T) {
	for _, protocol := range []string{"vnc", "rdp"} {
		t.Run(protocol, func(t *testing.T) {
			svc, kube, sessions := newConnectionFixture(t)
			ctx := context.Background()
			ws := seedDesktopWorkspace(t, kube, sessions, desktopServiceTemplate(), protocol)
			if err := kube.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "waas-desktop-" + ws.Name, Namespace: testNS},
				Data:       map[string][]byte{"password": []byte("generated-pw")},
			}); err != nil {
				t.Fatal(err)
			}

			info, err := svc.ConnectionInfo(ctx, "s-"+protocol)
			if err != nil {
				t.Fatalf("resolving connection info: %v", err)
			}
			if info.Protocol != protocol {
				t.Fatalf("expected protocol %s, got %s", protocol, info.Protocol)
			}
			if info.Password != "generated-pw" {
				t.Fatalf("password must come from the generated secret, got %q", info.Password)
			}
			if info.Username != "waas_user" {
				t.Fatalf("username must default to waas_user, got %q", info.Username)
			}
		})
	}
}

// An explicit credentialsSecretRef wins: no generated fallback is read
// and no username defaulting overwrites the secret's own value.
func TestConnectionInfoExplicitSecretWins(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	ctx := context.Background()
	tpl := desktopServiceTemplate()
	tpl.Spec.Protocols[0].CredentialsSecretRef = "desk-creds"
	ws := seedDesktopWorkspace(t, kube, sessions, tpl, "vnc")
	if err := kube.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "desk-creds", Namespace: testNS},
		Data:       map[string][]byte{"username": []byte("alice"), "password": []byte("known-pw")},
	}); err != nil {
		t.Fatal(err)
	}
	// A generated secret must NOT be consulted when the explicit one
	// resolved; plant a divergent one to catch any regression.
	if err := kube.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "waas-desktop-" + ws.Name, Namespace: testNS},
		Data:       map[string][]byte{"password": []byte("wrong")},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := svc.ConnectionInfo(ctx, "s-vnc")
	if err != nil {
		t.Fatalf("resolving connection info: %v", err)
	}
	if info.Username != "alice" || info.Password != "known-pw" {
		t.Fatalf("explicit credentials secret must win, got %q/%q", info.Username, info.Password)
	}
}

// Literal env passwords are dead on the platform: a template carrying
// WAAS_DESKTOP_PASSWORD as a literal is NOT read, and without any Secret the resolution
// fails hard instead of silently connecting with a legacy value.
func TestConnectionInfoIgnoresLiteralTemplateEnv(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	tpl := desktopServiceTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_DESKTOP_PASSWORD", Value: "legacy-literal"}}
	seedDesktopWorkspace(t, kube, sessions, tpl, "vnc")

	info, err := svc.ConnectionInfo(context.Background(), "s-vnc")
	if err == nil {
		t.Fatalf("literal template env must not resolve a connection, got %+v", info)
	}
}

// --- Generated ssh keypair fallback (shared predicate with the operator) ---

func sshServiceTemplate() *waasv1alpha1.WorkspaceTemplate {
	tpl := desktopServiceTemplate()
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "ssh", Port: 2222, Default: true},
		{Name: "vnc", Port: 5901},
	}
	return tpl
}

// seedSSHWorkspace is seedDesktopWorkspace with ssh in the status list.
func seedSSHWorkspace(t *testing.T, kube client.WithWatch, sessions repository.SessionRepository,
	tpl *waasv1alpha1.WorkspaceTemplate) *waasv1alpha1.Workspace {
	t.Helper()
	ws := seedDesktopWorkspace(t, kube, sessions, tpl, "ssh")
	ws.Status.Protocols = append(ws.Status.Protocols, waasv1alpha1.WorkspaceProtocolStatus{Name: "ssh", Port: 2222})
	if err := kube.Status().Update(context.Background(), ws); err != nil {
		t.Fatalf("adding ssh to status: %v", err)
	}
	return ws
}

// With no explicit source, ssh resolves the operator's waas-ssh-<name>
// Secret: the private key lands in guacd params and the username
// defaults to the waas-images system account.
func TestConnectionInfoGeneratedSSHKey(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	ctx := context.Background()
	ws := seedSSHWorkspace(t, kube, sessions, sshServiceTemplate())
	if err := kube.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "waas-ssh-" + ws.Name, Namespace: testNS},
		Data: map[string][]byte{
			"private-key": []byte("PEM-PRIVATE"),
			"public-key":  []byte("ssh-ed25519 AAAA generated"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := svc.ConnectionInfo(ctx, "s-ssh")
	if err != nil {
		t.Fatalf("ConnectionInfo: %v", err)
	}
	if info.Protocol != "ssh" || info.Port != 2222 {
		t.Fatalf("expected the ssh endpoint, got %s:%d", info.Protocol, info.Port)
	}
	if info.Params["private-key"] != "PEM-PRIVATE" {
		t.Fatalf("expected the generated private key in guacd params, got %q", info.Params["private-key"])
	}
	if info.Username != "waas_user" {
		t.Fatalf("ssh must default to the waas-images system account, got %q", info.Username)
	}
}

// An explicit credentialsSecretRef still wins: the generated fallback
// must not overwrite it (nor even be consulted — private-key is set).
func TestConnectionInfoSSHCredentialsSecretWins(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	ctx := context.Background()
	tpl := sshServiceTemplate()
	tpl.Spec.Protocols[0].CredentialsSecretRef = "ssh-creds"
	ws := seedSSHWorkspace(t, kube, sessions, tpl)
	for name, key := range map[string]string{"ssh-creds": "EXPLICIT", "waas-ssh-" + ws.Name: "GENERATED"} {
		if err := kube.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
			Data:       map[string][]byte{"private-key": []byte(key), "username": []byte("admin")},
		}); err != nil {
			t.Fatal(err)
		}
	}

	info, err := svc.ConnectionInfo(ctx, "s-ssh")
	if err != nil {
		t.Fatalf("ConnectionInfo: %v", err)
	}
	if info.Params["private-key"] != "EXPLICIT" || info.Username != "admin" {
		t.Fatalf("credentialsSecretRef must win, got key=%q user=%q", info.Params["private-key"], info.Username)
	}
}

// A template with an explicit WAAS_SSH_AUTHORIZED_KEYS env flips the
// shared predicate: the fallback never touches the (absent) Secret and
// the connection proceeds without a private key instead of hard-failing.
func TestConnectionInfoSSHExplicitEnvSkipsFallback(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	tpl := sshServiceTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_SSH_AUTHORIZED_KEYS", Value: "ssh-ed25519 AAAA admin"}}
	seedSSHWorkspace(t, kube, sessions, tpl)

	info, err := svc.ConnectionInfo(context.Background(), "s-ssh")
	if err != nil {
		t.Fatalf("a false predicate must not hard-fail on the absent Secret: %v", err)
	}
	if got := info.Params["private-key"]; got != "" {
		t.Fatalf("no key may be resolved, got %q", got)
	}
}

// When the predicate says a keypair was generated, a missing Secret is a
// hard error — connecting keyless when the pod authorizes a generated
// key would be worse.
func TestConnectionInfoSSHMissingSecretFails(t *testing.T) {
	svc, kube, sessions := newConnectionFixture(t)
	seedSSHWorkspace(t, kube, sessions, sshServiceTemplate())

	if info, err := svc.ConnectionInfo(context.Background(), "s-ssh"); err == nil {
		t.Fatalf("expected a hard error on the missing generated Secret, got %+v", info)
	}
}
