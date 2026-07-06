package service

import (
	"context"
	"path/filepath"
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

const testNS = "waas-workspaces"

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
	return NewGovernanceService(kube, testNS, users, audit)
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
