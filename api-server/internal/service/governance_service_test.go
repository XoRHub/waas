package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

const testNS = "test-ns"

// newGovernanceFixture wires a GovernanceService against SQLite + the fake
// cluster, seeded with the given users and policies. This is the
// integration layer the unit tests in operator/pkg/policy cannot cover:
// identity comes from the DB group mirror, exactly like production.
func newGovernanceFixture(t *testing.T, usersIn []model.User, policies []waasv1alpha1.WorkspacePolicy) *GovernanceService {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "gov.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	users := repository.NewSQLUserRepository(db)
	now := time.Now().UTC()
	for i := range usersIn {
		u := usersIn[i]
		u.Role, u.Active, u.CreatedAt, u.UpdatedAt = auth.RoleUser, true, now, now
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

	audit := NewAuditService(repository.NewSQLAuditRepository(db))
	catalogRepo := repository.NewSQLCatalogRepository(db)
	return NewGovernanceService(kube, testNS, users, audit, catalogRepo)
}

func pol(name string, priority int32, maxWS int32, subjects ...waasv1alpha1.PolicySubject) waasv1alpha1.WorkspacePolicy {
	return waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Priority: priority,
			Subjects: subjects,
			Limits:   waasv1alpha1.PolicyLimits{MaxWorkspaces: &maxWS},
		},
	}
}

func group(name string) waasv1alpha1.PolicySubject {
	return waasv1alpha1.PolicySubject{Kind: waasv1alpha1.SubjectGroup, Name: name}
}

func userSubject(name string) waasv1alpha1.PolicySubject {
	return waasv1alpha1.PolicySubject{Kind: waasv1alpha1.SubjectUser, Name: name}
}

// TestEffectivePolicyResolutionMatrix covers the DB-identity → policy
// resolution chain for every class of user the platform knows.
func TestEffectivePolicyResolutionMatrix(t *testing.T) {
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("default", 0, 1),
		pol("power-user", 100, 5, group("nymphe:dev"), group("nymphe:ops")),
		pol("data-team", 200, 8, group("nymphe:data")),
		pol("vip-marc", 1000, 20, userSubject("marc")),
	}
	users := []model.User{
		{ID: "u-none", Username: "nobody", Groups: nil},
		{ID: "u-dev", Username: "devuser", Groups: []string{"nymphe:dev"}},
		{ID: "u-multi", Username: "multi", Groups: []string{"nymphe:dev", "nymphe:data"}},
		{ID: "u-marc", Username: "marc", Groups: []string{"nymphe:dev"}},
	}
	svc := newGovernanceFixture(t, users, policies)
	ctx := context.Background()

	cases := []struct {
		userID   string
		expected string
		via      string
	}{
		// No groups: only the subjects-less default matches.
		{"u-none", "default", "*"},
		// One group: the group policy outranks default.
		{"u-dev", "power-user", "Group:nymphe:dev"},
		// Several groups matching several policies: highest priority wins
		// as a whole, no field merging.
		{"u-multi", "data-team", "Group:nymphe:data"},
		// Nominative User subject outranks every group policy.
		{"u-marc", "vip-marc", "User:marc"},
	}
	for _, tc := range cases {
		report, err := svc.AdminEffectivePolicy(ctx, tc.userID)
		if err != nil {
			t.Fatalf("%s: %v", tc.userID, err)
		}
		if report.Effective == nil || report.Effective.Name != tc.expected {
			t.Fatalf("%s: expected policy %q, got %+v (denial=%q)", tc.userID, tc.expected, report.Effective, report.Denial)
		}
		var selected *model.EvaluatedPolicy
		for i := range report.Evaluated {
			if report.Evaluated[i].Selected {
				selected = &report.Evaluated[i]
			}
		}
		if selected == nil || selected.Name != tc.expected {
			t.Fatalf("%s: evaluated list does not flag %q as selected: %+v", tc.userID, tc.expected, report.Evaluated)
		}
		if selected.Via != tc.via {
			t.Fatalf("%s: expected via %q, got %q", tc.userID, tc.via, selected.Via)
		}
		if len(report.Evaluated) != len(policies) {
			t.Fatalf("%s: expected %d evaluated policies, got %d", tc.userID, len(policies), len(report.Evaluated))
		}
	}
}

// TestCatalogListsTemplatesOfEveryProtocol is the non-regression test for
// "SSH templates invisible at workspace creation": the portal derives its
// template list from Catalog().Templates, so every protocol's template must
// surface there as long as the policy allows the image — and a policy that
// omits an image must be the ONLY thing that hides its templates.
func TestCatalogListsTemplatesOfEveryProtocol(t *testing.T) {
	openPolicy := waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		// Images empty = whole catalog.
		Spec: waasv1alpha1.WorkspacePolicySpec{Priority: 0},
	}
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{openPolicy})
	ctx := context.Background()

	seed := []struct {
		name      string
		image     string
		protocols []waasv1alpha1.Protocol
	}{
		{"img-vnc", "reg/xfce:1", []waasv1alpha1.Protocol{waasv1alpha1.ProtocolVNC, waasv1alpha1.ProtocolRDP}},
		{"img-rdp", "reg/win:1", []waasv1alpha1.Protocol{waasv1alpha1.ProtocolRDP}},
		{"img-ssh", "reg/dev-ssh:1", []waasv1alpha1.Protocol{waasv1alpha1.ProtocolSSH}},
	}
	for _, s := range seed {
		img := &waasv1alpha1.WorkspaceImage{
			ObjectMeta: metav1.ObjectMeta{Name: s.name, Namespace: testNS},
			Spec: waasv1alpha1.WorkspaceImageSpec{
				DisplayName: s.name, Image: s.image, Protocols: s.protocols, Enabled: true,
			},
		}
		if err := svc.kube.Create(ctx, img); err != nil {
			t.Fatalf("seeding image %s: %v", s.name, err)
		}
	}
	templates := []struct {
		name     string
		image    string
		protocol string
		port     int32
	}{
		{"tpl-vnc", "reg/xfce:1", "vnc", 5901},
		{"tpl-rdp", "reg/win:1", "rdp", 3389},
		{"tpl-ssh", "reg/dev-ssh:1", "ssh", 2222},
	}
	for _, tc := range templates {
		tpl := &waasv1alpha1.WorkspaceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: testNS},
			Spec: waasv1alpha1.WorkspaceTemplateSpec{
				DisplayName: tc.name, OS: waasv1alpha1.OSLinux, Image: tc.image,
				Protocols: []waasv1alpha1.WorkspaceProtocol{{Name: tc.protocol, Port: tc.port, Default: true}},
			},
		}
		if err := svc.kube.Create(ctx, tpl); err != nil {
			t.Fatalf("seeding template %s: %v", tc.name, err)
		}
	}

	catalog, err := svc.Catalog(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	attached := map[string]bool{}
	for _, img := range catalog {
		for _, name := range img.Templates {
			attached[name] = true
		}
	}
	for _, tc := range templates {
		if !attached[tc.name] {
			t.Fatalf("template %s (protocol %s) missing from the catalog projection: %+v", tc.name, tc.protocol, catalog)
		}
	}

	// The inverse mechanism (how the bug happened): a policy whose images
	// list omits the ssh image hides its template from the projection.
	restrictive := &waasv1alpha1.WorkspacePolicy{}
	if err := svc.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "default"}, restrictive); err != nil {
		t.Fatal(err)
	}
	restrictive.Spec.Images = []string{"img-vnc", "img-rdp"}
	if err := svc.kube.Update(ctx, restrictive); err != nil {
		t.Fatal(err)
	}
	catalog, err = svc.Catalog(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range catalog {
		if img.Name == "img-ssh" {
			t.Fatalf("policy restriction must exclude img-ssh, got %+v", catalog)
		}
	}
}

func TestEffectivePolicyPriorityTieWarns(t *testing.T) {
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("aaa-team", 100, 3, group("g1")),
		pol("bbb-team", 100, 4, group("g1")),
	}
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1", Groups: []string{"g1"}}}, policies)

	report, err := svc.AdminEffectivePolicy(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Effective == nil || report.Effective.Name != "aaa-team" {
		t.Fatalf("tie must break on the lexicographically smallest name, got %+v", report.Effective)
	}
	if len(report.Warnings) == 0 || !strings.Contains(report.Warnings[0], "priority") {
		t.Fatalf("expected a same-priority warning, got %v", report.Warnings)
	}
}

func TestEffectivePolicyFailsClosedWithoutAnyMatch(t *testing.T) {
	// No subjects-less default: a user with no matching group must be
	// denied, and the report must say so instead of inventing a policy.
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("power-user", 100, 5, group("nymphe:dev")),
	}
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1", Groups: []string{"other"}}}, policies)

	report, err := svc.AdminEffectivePolicy(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Effective != nil {
		t.Fatalf("expected no effective policy, got %q", report.Effective.Name)
	}
	if report.Denial == "" {
		t.Fatal("expected an explicit fail-closed denial message")
	}

	// And the user-facing quota view degrades the same way.
	quota, err := svc.Quota(context.Background(), Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if quota.Policy != "" {
		t.Fatalf("quota must reflect the fail-closed state, got policy %q", quota.Policy)
	}
}

// TestQuotaFollowsGroupMirror is the regression test for the original bug:
// stale/empty groups silently downgraded everyone to the default policy.
// Updating the mirror (as SSO login or the admin editor does) must change
// the resolved policy immediately.
func TestQuotaFollowsGroupMirror(t *testing.T) {
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("default", 0, 1),
		pol("power-user", 100, 5, group("nymphe:ops")),
	}
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "admin1"}}, policies)
	ctx := context.Background()

	quota, err := svc.Quota(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if quota.Policy != "default" {
		t.Fatalf("without groups the default policy must apply, got %q", quota.Policy)
	}

	// Mirror refresh (what OIDC login / the admin group editor performs).
	user, err := svc.users.FindByID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	user.Groups = []string{"nymphe:ops"}
	if err := svc.users.Update(ctx, user); err != nil {
		t.Fatal(err)
	}

	quota, err = svc.Quota(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if quota.Policy != "power-user" {
		t.Fatalf("after the group mirror refresh the group policy must apply, got %q", quota.Policy)
	}
	if quota.MaxWorkspaces == nil || *quota.MaxWorkspaces != 5 {
		t.Fatalf("expected power-user limits to apply, got %+v", quota.MaxWorkspaces)
	}
}

// TestQuotaCountsRunningWorkspaces: paused workspaces keep an ownership
// slot (usedWorkspaces) but free a running slot (runningWorkspaces).
func TestQuotaCountsRunningWorkspaces(t *testing.T) {
	p := pol("default", 0, 5)
	three := int32(3)
	p.Spec.Limits.MaxRunningWorkspaces = &three
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{p})
	ctx := context.Background()

	for _, ws := range []struct {
		name   string
		paused bool
	}{{"w-run-1", false}, {"w-run-2", false}, {"w-paused", true}} {
		obj := &waasv1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{Name: ws.name, Namespace: testNS},
			Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1", Paused: ws.paused},
		}
		if err := svc.kube.Create(ctx, obj); err != nil {
			t.Fatalf("seeding workspace %s: %v", ws.name, err)
		}
	}

	quota, err := svc.Quota(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if quota.MaxRunningWorkspaces == nil || *quota.MaxRunningWorkspaces != 3 {
		t.Fatalf("expected maxRunningWorkspaces=3, got %+v", quota.MaxRunningWorkspaces)
	}
	if quota.UsedWorkspaces != 3 {
		t.Fatalf("paused workspaces must keep an ownership slot: got %d", quota.UsedWorkspaces)
	}
	if quota.RunningWorkspaces != 2 {
		t.Fatalf("paused workspaces must not count as running: got %d", quota.RunningWorkspaces)
	}
}

// TestUpsertPolicyRoundTripsMaxRunning pins the admin editor mapping in
// both directions (model → CR spec → model).
func TestUpsertPolicyRoundTripsMaxRunning(t *testing.T) {
	svc := newGovernanceFixture(t, nil, nil)
	two := int32(2)
	m, err := svc.AdminUpsertPolicy(context.Background(), Actor{ID: "admin"}, "p1", UpsertPolicyInput{
		Limits: model.PolicyLimitsModel{MaxRunningWorkspaces: &two},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Limits.MaxRunningWorkspaces == nil || *m.Limits.MaxRunningWorkspaces != 2 {
		t.Fatalf("maxRunningWorkspaces must round-trip through the admin editor, got %+v", m.Limits.MaxRunningWorkspaces)
	}
}

// registryImageInput is a minimal valid registry-mode upsert payload
// carrying the given catalog block.
func registryImageInput(catalog *model.CatalogSourceModel) UpsertImageInput {
	return UpsertImageInput{
		DisplayName: "XorHub images",
		Registry:    "docker.io/xorhub",
		Protocols:   []string{"vnc"},
		Catalog:     catalog,
	}
}

// TestUpsertImageCatalogValidation mirrors the CRD's XValidations at
// the API layer: exactly one from source, auth only with from.url —
// plus the normalization contract (an all-empty block, the untouched
// scaffold, means "no catalog").
func TestUpsertImageCatalogValidation(t *testing.T) {
	cases := []struct {
		name    string
		catalog *model.CatalogSourceModel
		wantErr string // substring of the BadRequest message; "" = accepted
	}{
		{"absent catalog is accepted", nil, ""},
		{"all-empty block (untouched scaffold) is accepted as no catalog",
			&model.CatalogSourceModel{
				From: model.CatalogSourceFrom{
					ConfigMapKeyRef: &model.CatalogConfigMapRef{},
					SecretKeyRef:    &model.CatalogSecretRef{},
				},
				Auth: &model.CatalogSourceAuth{BearerToken: &model.CatalogBearerToken{}},
			}, ""},
		{"url source is accepted",
			&model.CatalogSourceModel{From: model.CatalogSourceFrom{URL: "https://example.com/catalog.yaml"}}, ""},
		{"auth with url is accepted",
			&model.CatalogSourceModel{
				From: model.CatalogSourceFrom{URL: "https://example.com/catalog.yaml"},
				Auth: &model.CatalogSourceAuth{BearerToken: &model.CatalogBearerToken{SecretRef: "catalog-token"}},
			}, ""},
		{"two sources are rejected",
			&model.CatalogSourceModel{From: model.CatalogSourceFrom{
				URL:             "https://example.com/catalog.yaml",
				ConfigMapKeyRef: &model.CatalogConfigMapRef{Name: "cm"},
			}}, "exactly one of url, configMapKeyRef, or secretKeyRef"},
		{"auth without any source is rejected",
			&model.CatalogSourceModel{
				Auth: &model.CatalogSourceAuth{BearerToken: &model.CatalogBearerToken{SecretRef: "catalog-token"}},
			}, "only meaningful when catalog.from.url is set"},
		{"auth with a non-url source is rejected",
			&model.CatalogSourceModel{
				From: model.CatalogSourceFrom{ConfigMapKeyRef: &model.CatalogConfigMapRef{Name: "cm"}},
				Auth: &model.CatalogSourceAuth{BearerToken: &model.CatalogBearerToken{SecretRef: "catalog-token"}},
			}, "only meaningful when catalog.from.url is set"},
		{"secretKeyRef without key is rejected",
			&model.CatalogSourceModel{
				From: model.CatalogSourceFrom{SecretKeyRef: &model.CatalogSecretRef{Name: "manifest"}},
			}, "secretKeyRef.key is required"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGovernanceFixture(t, nil, nil)
			name := fmt.Sprintf("img-%d", i)
			m, err := svc.AdminUpsertImage(context.Background(), Actor{ID: "admin"}, name, registryImageInput(tc.catalog))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if m != nil || err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected BadRequest containing %q, got (%+v, %v)", tc.wantErr, m, err)
			}
		})
	}
}

// TestUpsertImageCatalogRoundTrips pins the editor contract in both
// directions: the created spec.catalog is echoed back as catalogSource,
// and an update whose payload carries catalog (as the editor's rename
// guarantees) keeps spec.catalog populated — no silent wipe.
func TestUpsertImageCatalogRoundTrips(t *testing.T) {
	svc := newGovernanceFixture(t, nil, nil)
	ctx := context.Background()
	in := registryImageInput(&model.CatalogSourceModel{
		From: model.CatalogSourceFrom{URL: "https://example.com/catalog.yaml"},
		Auth: &model.CatalogSourceAuth{BearerToken: &model.CatalogBearerToken{SecretRef: "catalog-token"}},
	})

	m, err := svc.AdminUpsertImage(ctx, Actor{ID: "admin"}, "xorhub", in)
	if err != nil {
		t.Fatal(err)
	}
	if m.CatalogSource == nil || m.CatalogSource.From.URL != "https://example.com/catalog.yaml" ||
		m.CatalogSource.Auth == nil || m.CatalogSource.Auth.BearerToken == nil ||
		m.CatalogSource.Auth.BearerToken.SecretRef != "catalog-token" {
		t.Fatalf("spec.catalog must be echoed back as catalogSource, got %+v", m.CatalogSource)
	}
	// The read-only status projection stays gated by spec.catalog too.
	if m.Catalog == nil {
		t.Fatalf("catalog status stub must be present when spec.catalog is set")
	}

	// Update carrying the echoed source under the payload key: the CR
	// keeps its sync source (the full-spec kube.Update must not wipe it).
	in.Catalog = m.CatalogSource
	in.Description = "edited"
	if _, err := svc.AdminUpsertImage(ctx, Actor{ID: "admin"}, "xorhub", in); err != nil {
		t.Fatal(err)
	}
	img := &waasv1alpha1.WorkspaceImage{}
	if err := svc.kube.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "xorhub"}, img); err != nil {
		t.Fatal(err)
	}
	if img.Spec.Description != "edited" {
		t.Fatalf("update must apply, got %+v", img.Spec)
	}
	if img.Spec.Catalog == nil || img.Spec.Catalog.From.URL != "https://example.com/catalog.yaml" ||
		img.Spec.Catalog.Auth == nil || img.Spec.Catalog.Auth.BearerToken == nil ||
		img.Spec.Catalog.Auth.BearerToken.SecretRef != "catalog-token" {
		t.Fatalf("an update carrying catalog must keep spec.catalog populated, got %+v", img.Spec.Catalog)
	}

	// A ConfigMap source round-trips the same way (key default left to
	// the reader: stored verbatim).
	cmIn := registryImageInput(&model.CatalogSourceModel{
		From: model.CatalogSourceFrom{ConfigMapKeyRef: &model.CatalogConfigMapRef{Name: "waas-catalog"}},
	})
	m, err = svc.AdminUpsertImage(ctx, Actor{ID: "admin"}, "static", cmIn)
	if err != nil {
		t.Fatal(err)
	}
	if m.CatalogSource == nil || m.CatalogSource.From.ConfigMapKeyRef == nil ||
		m.CatalogSource.From.ConfigMapKeyRef.Name != "waas-catalog" {
		t.Fatalf("configMapKeyRef source must round-trip, got %+v", m.CatalogSource)
	}
}

// TestCatalogSurfacesDiscoveredEntries: the discovered entries of a
// registry-mode image (catalog_entries, written by CatalogSyncWorker)
// ride on the SAME visibility gate as the CatalogImage itself
// (policy.AllowedImages) — nested under the image, no separate list,
// no separate filter.
func TestCatalogSurfacesDiscoveredEntries(t *testing.T) {
	openPolicy := waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec:       waasv1alpha1.WorkspacePolicySpec{Priority: 0},
	}
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1"}}, []waasv1alpha1.WorkspacePolicy{openPolicy})
	ctx := context.Background()

	visible := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "waas-images", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XorHub images", Registry: "ghcr.io/xorhub/waas-images",
			Protocols: []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC}, Enabled: true,
		},
		Status: waasv1alpha1.WorkspaceImageStatus{
			Catalog: &waasv1alpha1.ImageCatalogStatus{Source: "Fetched"},
		},
	}
	// Same catalog data on a group-restricted image the user is NOT in:
	// its discovered entries must vanish with the image itself.
	hidden := visible.DeepCopy()
	hidden.Name = "restricted-images"
	hidden.Spec.AllowedGroups = []string{"ops-only"}
	for _, img := range []*waasv1alpha1.WorkspaceImage{visible, hidden} {
		if err := svc.kube.Create(ctx, img); err != nil {
			t.Fatalf("seeding image %s: %v", img.Name, err)
		}
		entry := []repository.CatalogEntry{{
			Image: "ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:def",
			OS:    "linux", App: "firefox", Icon: "firefox", SyncedAt: time.Now(),
			Profile:     "hardened",
			Recommended: json.RawMessage(`{"podSecurityContext":{"runAsUser":1000},"env":[{"name":"WAAS_AUDIO_ENABLED","protocols":["vnc"]}]}`),
		}}
		if err := svc.catalog.ReplaceEntries(ctx, img.Name, entry); err != nil {
			t.Fatalf("seeding catalog entries of %s: %v", img.Name, err)
		}
	}

	catalog, err := svc.Catalog(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Name != "waas-images" {
		t.Fatalf("expected only the unrestricted image, got %+v", catalog)
	}
	got := catalog[0].Discovered
	if len(got) != 1 || got[0].Icon != "firefox" || got[0].OS != "linux" {
		t.Fatalf("discovered entries not surfaced verbatim: %+v", got)
	}
	if got[0].Profile != "hardened" || got[0].Recommended == nil ||
		got[0].Recommended.PodSecurityContext == nil || got[0].Recommended.PodSecurityContext.RunAsUser == nil || *got[0].Recommended.PodSecurityContext.RunAsUser != 1000 {
		t.Fatalf("profile/recommended not surfaced: %+v", got[0])
	}
	// Per-image protocols derive from the hint tags (vnc here), not the
	// parent entry's list (kasmvnc) — the single-app-image fix.
	if !slices.Equal(got[0].Protocols, []string{"vnc"}) {
		t.Fatalf("per-image protocols must derive from env-hint tags, got %v", got[0].Protocols)
	}
}

// TestDeriveProtocols: a discovered image's protocol surface is the
// canonical-order (params.Protocols()) union of its env hints' protocol
// tags. No tagged hints (nil recommendation, hint-less recommendation,
// or only unscoped hints) derives to EMPTY here — the server never
// invents the parent list; the frontend applies the entry-level
// fallback. kasmvnc never derives (entry-level only, by nature) — that
// fallback is how a kasmvnc image reaches the prefill.
func TestDeriveProtocols(t *testing.T) {
	rec := func(protos ...[]string) *model.DeploymentRecommendation {
		r := &model.DeploymentRecommendation{}
		for _, p := range protos {
			r.Env = append(r.Env, model.RecommendedEnvVar{Name: "E", Protocols: p})
		}
		return r
	}
	cases := []struct {
		name string
		rec  *model.DeploymentRecommendation
		want []string
	}{
		{"nil recommendation derives empty", nil, nil},
		{"hint-less recommendation derives empty", rec(), nil},
		{"unscoped-only hints derive empty", rec(nil), nil},
		{"vnc-only hints (hermes-agent shape)", rec([]string{"vnc"}), []string{"vnc"}},
		{"vnc + ssh across two hints", rec([]string{"vnc"}, []string{"ssh"}), []string{"vnc", "ssh"}},
		{"vnc + ssh on one multi-tagged hint", rec([]string{"vnc", "ssh"}), []string{"vnc", "ssh"}},
		{"all three across hints (full-desktop shape)", rec([]string{"ssh"}, []string{"vnc"}, []string{"rdp"}), []string{"vnc", "rdp", "ssh"}},
		{"duplicate tags dedupe, order canonical", rec([]string{"ssh", "vnc"}, []string{"rdp", "vnc"}), []string{"vnc", "rdp", "ssh"}},
		{"kasmvnc tags never derive", rec([]string{"kasmvnc"}), nil},
		{"kasmvnc dropped from a mixed hint", rec([]string{"vnc", "kasmvnc"}), []string{"vnc"}},
		{"unknown tags kept deterministically last", rec([]string{"zz", "vnc", "aa"}), []string{"vnc", "aa", "zz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveProtocols(tc.rec); !slices.Equal(got, tc.want) {
				t.Fatalf("deriveProtocols() = %v, want %v", got, tc.want)
			}
		})
	}
}
