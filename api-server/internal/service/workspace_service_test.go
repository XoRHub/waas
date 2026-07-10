package service

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

// TestLegacyTemplateAlwaysExposesAConnection is the non-regression test
// for "dev firefox template shows an empty connection field": a template
// with no protocols block (legacy, OS-derived) must still surface exactly
// one default protocol in its API projection — the UI always has a
// connection to display, never a silent empty box.
func TestLegacyTemplateAlwaysExposesAConnection(t *testing.T) {
	tpl := &waasv1alpha1.WorkspaceTemplate{
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "Firefox (isolated browsing)",
			OS:          waasv1alpha1.OSLinux,
			Image:       "reg/firefox:1",
			Port:        5901,
			// No Protocols: the legacy shape that triggered the bug.
		},
	}
	m := templateToModel(tpl)
	if len(m.Protocols) != 1 {
		t.Fatalf("legacy template must synthesize exactly one protocol, got %+v", m.Protocols)
	}
	p := m.Protocols[0]
	if p.Name != "vnc" || p.Port != 5901 || !p.Default {
		t.Fatalf("expected default vnc:5901, got %+v", p)
	}

	// Windows legacy templates derive rdp:3389 the same way.
	tpl.Spec.OS = waasv1alpha1.OSWindows
	tpl.Spec.Port = 0
	m = templateToModel(tpl)
	if len(m.Protocols) != 1 || m.Protocols[0].Name != "rdp" || m.Protocols[0].Port != 3389 {
		t.Fatalf("expected default rdp:3389 for windows, got %+v", m.Protocols)
	}
}

// TestOverridesSummaryIsAuditSafe pins the audit contract: field names
// and env var NAMES appear, env VALUES never do (they may carry
// credentials like VNC_PW).
func TestOverridesSummaryIsAuditSafe(t *testing.T) {
	if got := overridesSummary(nil); got != "" {
		t.Fatalf("nil overrides must produce no audit line, got %q", got)
	}
	if got := overridesSummary(&waasv1alpha1.WorkspaceOverrides{}); got != "" {
		t.Fatalf("empty overrides must produce no audit line, got %q", got)
	}

	ov := &waasv1alpha1.WorkspaceOverrides{
		Env: []corev1.EnvVar{
			{Name: "VNC_PW", Value: "super-secret"},
			{Name: "HTTP_PROXY", Value: "http://proxy:3128"},
		},
		Protocol:     "rdp",
		NodeSelector: map[string]string{"zone": "a"},
	}
	got := overridesSummary(ov)
	for _, want := range []string{"env=VNC_PW,HTTP_PROXY", "protocol=rdp", "nodeSelector"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q must contain %q", got, want)
		}
	}
	if strings.Contains(got, "super-secret") || strings.Contains(got, "proxy:3128") {
		t.Fatalf("summary %q leaks env values", got)
	}
}

// TestEffectiveKasmVNCConfig pins the read path of the operator-
// materialized kasmvnc.yaml: the ConfigMap is addressed via the CRD's own
// EffectiveWorkloadName/EffectiveTargetNamespace (never a re-derived
// naming convention), the owner and admins may read it, any other user
// gets the same 404 as Get (no existence leak), and a workspace without
// the ConfigMap (non-kasmvnc, or not reconciled yet) is a clean 404.
func TestEffectiveKasmVNCConfig(t *testing.T) {
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}
	svc := &WorkspaceService{kube: kube, namespace: "waas"}
	ctx := context.Background()

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "cad", Namespace: "waas", UID: "uid-cad"},
		Spec: waasv1alpha1.WorkspaceSpec{
			TemplateRef: "kasm", Owner: "u1",
			TargetNamespace: "waas-alice", WorkloadName: "cad-station",
		},
	}
	if err := kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	// The ConfigMap the operator would materialize: workload name, target
	// namespace, kasmvnc.yaml key.
	effective := "data_loss_prevention:\n  clipboard:\n    server_to_client:\n      enabled: false\n"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cad-station", Namespace: "waas-alice"},
		Data:       map[string]string{"kasmvnc.yaml": effective},
	}
	if err := kube.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}

	owner := Actor{ID: "u1", Role: "user"}
	got, err := svc.EffectiveKasmVNCConfig(ctx, owner, "uid-cad")
	if err != nil || got != effective {
		t.Fatalf("owner must read the effective config, got %q, %v", got, err)
	}
	admin := Actor{ID: "root", Role: "admin"}
	if got, err = svc.EffectiveKasmVNCConfig(ctx, admin, "uid-cad"); err != nil || got != effective {
		t.Fatalf("admin must read the effective config, got %q, %v", got, err)
	}
	// Another user: same 404 as Get — never a 403 leaking existence.
	if _, err = svc.EffectiveKasmVNCConfig(ctx, Actor{ID: "u2", Role: "user"}, "uid-cad"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("non-owner must get not-found, got %v", err)
	}

	// Workspace without a materialized ConfigMap (guacd template, or the
	// operator has not reconciled yet): clean 404, not a 500.
	plain := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "waas", UID: "uid-xfce"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := kube.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.EffectiveKasmVNCConfig(ctx, owner, "uid-xfce"); err == nil ||
		!strings.Contains(err.Error(), "no KasmVNC configuration") {
		t.Fatalf("workspace without config must get a clean 404, got %v", err)
	}
}

// TestResolveWorkloadName pins the naming contract of point "Deployment =
// workspace name": sanitized display name, deterministic suffix only on
// collision, and collisions scoped to the target namespace.
func TestResolveWorkloadName(t *testing.T) {
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}
	svc := &WorkspaceService{kube: kube, namespace: "waas"}
	ctx := context.Background()

	existing := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "cr1", Namespace: "waas"},
		Spec: waasv1alpha1.WorkspaceSpec{
			TemplateRef: "xfce", Owner: "u1",
			TargetNamespace: "waas-alice", WorkloadName: "cad-station",
		},
	}
	if err := kube.Create(ctx, existing); err != nil {
		t.Fatal(err)
	}

	// Accents fold, spaces dash: fresh name in a fresh namespace.
	got, err := svc.resolveWorkloadName(ctx, "cr2", "Zoé «Test»", "waas-zoe")
	if err != nil || got != "zoe-test" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Collision in the same namespace gets the deterministic suffix.
	got, err = svc.resolveWorkloadName(ctx, "cr2", "CAD Station", "waas-alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "cad-station-") || len(got) != len("cad-station")+6 {
		t.Fatalf("expected suffixed name, got %q", got)
	}
	// Same display name in ANOTHER namespace needs no suffix.
	got, err = svc.resolveWorkloadName(ctx, "cr3", "CAD Station", "waas-bob")
	if err != nil || got != "cad-station" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Legacy names are protected too: "ws-<cr>" counts as taken.
	legacy := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: "waas"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := kube.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	got, err = svc.resolveWorkloadName(ctx, "cr4", "ws-old", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == "ws-old" {
		t.Fatalf("legacy deployment name must not be reused, got %q", got)
	}
}

// TestListResolvesOwnerUsernamesForAdmins pins the fleet-grouping
// contract: admins get OwnerUsername resolved per row (best-effort — a
// deleted owner leaves it empty without failing the List), non-admins
// never pay the lookup since they only see their own workspaces.
func TestListResolvesOwnerUsernamesForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{
		{ID: "admin1", Username: "boss", Role: auth.RoleAdmin},
		{ID: "u1", Username: "alice"},
	}, nil)

	seed := func(name, owner string) {
		ws := &waasv1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
				Labels:    map[string]string{ownerLabel: owner},
			},
			Spec: waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: owner},
		}
		if err := f.kube.Create(ctx, ws); err != nil {
			t.Fatalf("seeding workspace %s: %v", name, err)
		}
	}
	seed("ws-admin", "admin1")
	seed("ws-alice", "u1")
	// Owner deleted from the DB after the CR was created.
	seed("ws-orphan", "ghost")

	admin := Actor{ID: "admin1", Username: "boss", Role: string(auth.RoleAdmin)}
	rows, err := f.workspace.List(ctx, admin)
	if err != nil {
		t.Fatalf("admin List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("admin must see all 3 workspaces, got %d", len(rows))
	}
	byName := map[string]model.Workspace{}
	for _, ws := range rows {
		byName[ws.Name] = ws
	}
	if got := byName["ws-alice"].OwnerUsername; got != "alice" {
		t.Fatalf("ws-alice owner username: want %q, got %q", "alice", got)
	}
	if got := byName["ws-admin"].OwnerUsername; got != "boss" {
		t.Fatalf("ws-admin owner username: want %q, got %q", "boss", got)
	}
	if got := byName["ws-orphan"].OwnerUsername; got != "" {
		t.Fatalf("deleted owner must leave OwnerUsername empty, got %q", got)
	}

	// Non-admin: own rows only, no username enrichment (the caller knows
	// their own name; skipping the lookup is deliberate).
	alice := Actor{ID: "u1", Username: "alice", Role: "user"}
	rows, err = f.workspace.List(ctx, alice)
	if err != nil {
		t.Fatalf("user List: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ws-alice" {
		t.Fatalf("alice must see only her workspace, got %+v", rows)
	}
	if rows[0].OwnerUsername != "" {
		t.Fatalf("non-admin rows must not carry OwnerUsername, got %q", rows[0].OwnerUsername)
	}
}
