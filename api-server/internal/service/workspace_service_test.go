package service

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
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
// credentials like WAAS_DESKTOP_PASSWORD).
func TestOverridesSummaryIsAuditSafe(t *testing.T) {
	if got := overridesSummary(nil); got != "" {
		t.Fatalf("nil overrides must produce no audit line, got %q", got)
	}
	if got := overridesSummary(&waasv1alpha1.WorkspaceOverrides{}); got != "" {
		t.Fatalf("empty overrides must produce no audit line, got %q", got)
	}

	ov := &waasv1alpha1.WorkspaceOverrides{
		Env: []corev1.EnvVar{
			{Name: "WAAS_DESKTOP_PASSWORD", Value: "super-secret"},
			{Name: "HTTP_PROXY", Value: "http://proxy:3128"},
		},
		Protocol:     "rdp",
		NodeSelector: map[string]string{"zone": "a"},
	}
	got := overridesSummary(ov)
	for _, want := range []string{"env=WAAS_DESKTOP_PASSWORD,HTTP_PROXY", "protocol=rdp", "nodeSelector"} {
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
// naming convention), only the owner may read it — any other actor,
// admin included, gets the same 404 as Get (no existence leak) — and a
// workspace without the ConfigMap (non-kasmvnc, or not reconciled yet)
// is a clean 404.
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
	// Any other actor, admin included: same 404 as Get — ownership is
	// strict on every by-ID action, and never a 403 leaking existence.
	admin := Actor{ID: "root", Role: "admin"}
	if _, err = svc.EffectiveKasmVNCConfig(ctx, admin, "uid-cad"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("non-owner admin must get not-found, got %v", err)
	}
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

// seedOwnedWorkspace stamps a bare Workspace CR for a given owner (label
// + spec), enough for the List/ownership tests.
func seedOwnedWorkspace(t *testing.T, f *remoteFixture, name, owner string) {
	t.Helper()
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			UID:       types.UID("uid-" + name),
			Labels:    map[string]string{ownerLabel: owner},
		},
		Spec: waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: owner},
	}
	if err := f.kube.Create(context.Background(), ws); err != nil {
		t.Fatalf("seeding workspace %s: %v", name, err)
	}
}

// TestListResolvesOwnerUsernamesForAdmins pins the two listing scopes.
// Fleet listing (all=true, /admin route): every workspace, OwnerUsername
// resolved per row (best-effort — a deleted owner leaves it empty
// without failing the List). Personal listing (all=false): the actor's
// OWN rows only — an admin's role never widens their "My Workspaces"
// page — and no username enrichment.
func TestListResolvesOwnerUsernamesForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{
		{ID: "admin1", Username: "boss", Role: auth.RoleAdmin},
		{ID: "u1", Username: "alice"},
	}, nil)

	seedOwnedWorkspace(t, f, "ws-admin", "admin1")
	seedOwnedWorkspace(t, f, "ws-alice", "u1")
	// Owner deleted from the DB after the CR was created.
	seedOwnedWorkspace(t, f, "ws-orphan", "ghost")

	admin := Actor{ID: "admin1", Username: "boss", Role: string(auth.RoleAdmin)}
	rows, err := f.workspace.List(ctx, admin, true)
	if err != nil {
		t.Fatalf("admin List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("fleet listing must see all 3 workspaces, got %d", len(rows))
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

	// The ADMIN's personal listing: own rows only — the fix's core
	// visibility contract for the "My Workspaces" page.
	rows, err = f.workspace.List(ctx, admin, false)
	if err != nil {
		t.Fatalf("admin personal List: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ws-admin" {
		t.Fatalf("admin's personal listing must only carry their own workspace, got %+v", rows)
	}

	// Non-admin: own rows only, no username enrichment (the caller knows
	// their own name; skipping the lookup is deliberate).
	alice := Actor{ID: "u1", Username: "alice", Role: "user"}
	rows, err = f.workspace.List(ctx, alice, false)
	if err != nil {
		t.Fatalf("user List: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "ws-alice" {
		t.Fatalf("alice must see only her workspace, got %+v", rows)
	}
	if rows[0].OwnerUsername != "" {
		t.Fatalf("personal rows must not carry OwnerUsername, got %q", rows[0].OwnerUsername)
	}
}

// TestByIDActionsAreStrictlyOwnerOnly pins the security core of the fix:
// every action routed through fetchByID (Get, pause, delete via the user
// route — and with them connect, overrides, reload, resize, events,
// kasmvnc-config) answers 404 to ANY non-owner, admin included. Fleet
// management goes through the dedicated /admin routes, never through an
// ownership bypass.
func TestByIDActionsAreStrictlyOwnerOnly(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{
		{ID: "admin1", Username: "boss", Role: auth.RoleAdmin},
		{ID: "u1", Username: "alice"},
	}, nil)
	seedOwnedWorkspace(t, f, "mine", "admin1")
	seedOwnedWorkspace(t, f, "alices", "u1")
	admin := Actor{ID: "admin1", Username: "boss", Role: string(auth.RoleAdmin)}

	// Another user's workspace: same 404 as for a plain non-owner.
	if _, err := f.workspace.Get(ctx, admin, "uid-alices"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("admin Get on another user's workspace must be not-found, got %v", err)
	}
	if _, err := f.workspace.SetPaused(ctx, admin, "uid-alices", true); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("admin pause on another user's workspace must be not-found, got %v", err)
	}
	if err := f.workspace.Delete(ctx, admin, "uid-alices", true, false); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("admin delete via the USER route must be not-found, got %v", err)
	}

	// Their own workspace keeps working — the role loses nothing there.
	if _, err := f.workspace.Get(ctx, admin, "uid-mine"); err != nil {
		t.Fatalf("admin Get on their own workspace: %v", err)
	}
	if _, err := f.workspace.SetPaused(ctx, admin, "uid-mine", true); err != nil {
		t.Fatalf("admin pause on their own workspace: %v", err)
	}
}

// TestAdminDeleteWorksAcrossOwners pins the fleet-delete path: asAdmin
// skips the ownership check (the route middleware guarantees the role),
// keepVolume=true never stamps the delete-home opt-in, and the audit
// line records the real owner with via=admin.
func TestAdminDeleteWorksAcrossOwners(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{
		{ID: "admin1", Username: "boss", Role: auth.RoleAdmin},
		{ID: "u1", Username: "alice"},
	}, nil)
	seedOwnedWorkspace(t, f, "alices", "u1")
	admin := Actor{ID: "admin1", Username: "boss", Role: string(auth.RoleAdmin)}

	if err := f.workspace.Delete(ctx, admin, "uid-alices", true, true); err != nil {
		t.Fatalf("admin fleet delete: %v", err)
	}
	ws := &waasv1alpha1.Workspace{}
	if err := f.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "alices"}, ws); err == nil {
		// The fake client has no finalizer machinery: still present means
		// the volume opt-in must NOT have been stamped.
		if ws.Annotations[waasv1alpha1.AnnotationDeleteHome] == "true" {
			t.Fatalf("fleet delete must never stamp the delete-home opt-in")
		}
	}
	logs, _, err := f.auditSvc.List(ctx, repository.AuditFilter{Action: "workspace.deleted"}, 1, 10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("expected one workspace.deleted audit line, got %d, %v", len(logs), err)
	}
	for _, want := range []string{"keepVolume=true", "owner=u1", "via=admin"} {
		if !strings.Contains(logs[0].Detail, want) {
			t.Fatalf("audit detail %q must contain %q", logs[0].Detail, want)
		}
	}
}

// TestListRetainedVolumesResolvesOwnerUsernamesForAdmins mirrors the
// workspace-list contract on the volumes side of the fleet: the admin
// listing (all=true) carries OwnerUsername best-effort, the personal
// listing never pays the lookup.
func TestListRetainedVolumesResolvesOwnerUsernamesForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{
		{ID: "admin1", Username: "boss", Role: auth.RoleAdmin},
		{ID: "u1", Username: "alice"},
	}, nil)

	seed := func(name, owner string) {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
				Labels: map[string]string{
					waasv1alpha1.LabelRetained: "true",
					waasv1alpha1.LabelOwner:    owner,
				},
			},
		}
		if err := f.kube.Create(ctx, pvc); err != nil {
			t.Fatalf("seeding pvc %s: %v", name, err)
		}
	}
	seed("home-alice", "u1")
	// Owner deleted from the DB after the volume was retained.
	seed("home-orphan", "ghost")

	admin := Actor{ID: "admin1", Username: "boss", Role: string(auth.RoleAdmin)}
	rows, err := f.workspace.ListRetainedVolumes(ctx, admin, true)
	if err != nil {
		t.Fatalf("admin ListRetainedVolumes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("admin must see both volumes, got %d", len(rows))
	}
	byName := map[string]model.RetainedVolume{}
	for _, v := range rows {
		byName[v.Name] = v
	}
	if got := byName["home-alice"].OwnerUsername; got != "alice" {
		t.Fatalf("home-alice owner username: want %q, got %q", "alice", got)
	}
	if got := byName["home-orphan"].OwnerUsername; got != "" {
		t.Fatalf("deleted owner must leave OwnerUsername empty, got %q", got)
	}

	// Personal listing: own volumes only, no username enrichment.
	alice := Actor{ID: "u1", Username: "alice", Role: "user"}
	rows, err = f.workspace.ListRetainedVolumes(ctx, alice, false)
	if err != nil {
		t.Fatalf("user ListRetainedVolumes: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "home-alice" {
		t.Fatalf("alice must see only her volume, got %+v", rows)
	}
	if rows[0].OwnerUsername != "" {
		t.Fatalf("personal rows must not carry OwnerUsername, got %q", rows[0].OwnerUsername)
	}
}
