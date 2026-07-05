package policy

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func qty(s string) *resource.Quantity { q := resource.MustParse(s); return &q }

func pol(name string, prio int32, subjects ...waasv1alpha1.PolicySubject) waasv1alpha1.WorkspacePolicy {
	return waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       waasv1alpha1.WorkspacePolicySpec{Priority: prio, Subjects: subjects},
	}
}

func group(name string) waasv1alpha1.PolicySubject {
	return waasv1alpha1.PolicySubject{Kind: waasv1alpha1.SubjectGroup, Name: name}
}

func user(name string) waasv1alpha1.PolicySubject {
	return waasv1alpha1.PolicySubject{Kind: waasv1alpha1.SubjectUser, Name: name}
}

func img(name, ref string, enabled bool, groups ...string) waasv1alpha1.WorkspaceImage {
	return waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName:   name,
			Image:         ref,
			Protocols:     []waasv1alpha1.Protocol{waasv1alpha1.ProtocolVNC},
			Enabled:       enabled,
			AllowedGroups: groups,
		},
	}
}

func TestResolvePriorityWins(t *testing.T) {
	id := Identity{Owner: "u1", Username: "alice", Groups: []string{"nymphe:dev"}}
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("default", 0),
		pol("power-user", 100, group("nymphe:dev")),
		pol("alice-exception", 1000, user("alice")),
	}
	got, warns, deny := Resolve(policies, id)
	if deny != nil {
		t.Fatalf("unexpected denial: %v", deny)
	}
	if got.Name != "alice-exception" {
		t.Fatalf("want alice-exception, got %s", got.Name)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
}

func TestResolveTieBreaksLexicographicWithWarning(t *testing.T) {
	id := Identity{Owner: "u1", Groups: []string{"a", "b"}}
	policies := []waasv1alpha1.WorkspacePolicy{
		pol("zeta", 100, group("a")),
		pol("alpha", 100, group("b")),
	}
	got, warns, deny := Resolve(policies, id)
	if deny != nil {
		t.Fatalf("unexpected denial: %v", deny)
	}
	if got.Name != "alpha" {
		t.Fatalf("tie-break should pick alpha, got %s", got.Name)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "distinct priorities") {
		t.Fatalf("expected ambiguity warning, got %v", warns)
	}
}

func TestResolveFailsClosed(t *testing.T) {
	id := Identity{Owner: "u1", Groups: []string{"nobody"}}
	policies := []waasv1alpha1.WorkspacePolicy{pol("dev-only", 100, group("nymphe:dev"))}
	_, _, deny := Resolve(policies, id)
	if deny == nil || deny.Reason != ReasonNoPolicy {
		t.Fatalf("expected NoPolicyMatches denial, got %v", deny)
	}
}

func TestResolveUserMatchesOwnerUUIDOrUsername(t *testing.T) {
	policies := []waasv1alpha1.WorkspacePolicy{pol("p", 10, user("uuid-42"))}
	if _, _, deny := Resolve(policies, Identity{Owner: "uuid-42"}); deny != nil {
		t.Fatalf("owner UUID should match: %v", deny)
	}
	policies = []waasv1alpha1.WorkspacePolicy{pol("p", 10, user("alice"))}
	if _, _, deny := Resolve(policies, Identity{Owner: "uuid-42", Username: "alice"}); deny != nil {
		t.Fatalf("username should match: %v", deny)
	}
}

func TestImageGates(t *testing.T) {
	id := Identity{Owner: "u1", Groups: []string{"staff"}}
	p := pol("p", 0)
	p.Spec.Images = []string{"firefox"}

	cases := []struct {
		name   string
		image  waasv1alpha1.WorkspaceImage
		reason Reason
	}{
		{"disabled", img("firefox", "r/ff:1", false), ReasonImageDisabled},
		{"wrong group", img("firefox", "r/ff:1", true, "nymphe:dev"), ReasonImageNotAllowed},
		{"outside policy subset", img("devtools", "r/dt:1", true), ReasonImageNotAllowed},
	}
	for _, tc := range cases {
		deny := ImageAllowed(&tc.image, &p, id)
		if deny == nil || deny.Reason != tc.reason {
			t.Errorf("%s: want %s, got %v", tc.name, tc.reason, deny)
		}
	}

	ok := img("firefox", "r/ff:1", true, "staff")
	if deny := ImageAllowed(&ok, &p, id); deny != nil {
		t.Fatalf("expected allowed, got %v", deny)
	}
}

func TestAllowedImagesFiltersCatalog(t *testing.T) {
	id := Identity{Owner: "u1", Groups: []string{"staff"}}
	p := pol("p", 0) // no image subset = whole catalog
	catalog := []waasv1alpha1.WorkspaceImage{
		img("a", "r/a:1", true),
		img("b", "r/b:1", false),
		img("c", "r/c:1", true, "other-group"),
	}
	got := AllowedImages(catalog, &p, id)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("want [a], got %v", got)
	}
}

func TestLoadOfPrecedence(t *testing.T) {
	tpl := &waasv1alpha1.WorkspaceTemplate{
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
			},
			HomeSize: qty("20Gi"),
		},
	}
	ws := &waasv1alpha1.Workspace{}

	load, ok := LoadOf(ws, tpl, nil)
	if !ok {
		t.Fatal("compute should be known from template limits")
	}
	if load.CPU.String() != "2" || load.Memory.String() != "4Gi" || load.Storage.String() != "20Gi" {
		t.Fatalf("limits should win: %+v", load)
	}

	// No template sizing at all: image default steps in.
	empty := &waasv1alpha1.WorkspaceTemplate{}
	image := img("x", "r/x:1", true)
	image.Spec.Resources = &waasv1alpha1.ImageResources{
		Default: &waasv1alpha1.ComputeSize{CPU: qty("1"), Memory: qty("2Gi")},
	}
	load, ok = LoadOf(ws, empty, &image)
	if !ok || load.CPU.String() != "1" || load.Storage.String() != DefaultHomeSize {
		t.Fatalf("image default + default home expected: ok=%v %+v", ok, load)
	}

	// Nothing anywhere: compute unknown.
	if _, ok = LoadOf(ws, empty, nil); ok {
		t.Fatal("compute should be unknown")
	}
}

func TestCheckLimits(t *testing.T) {
	image := img("x", "r/x:1", true)
	image.Spec.Resources = &waasv1alpha1.ImageResources{
		Min: &waasv1alpha1.ComputeSize{Memory: qty("512Mi")},
		Max: &waasv1alpha1.ComputeSize{CPU: qty("2")},
	}
	p := pol("quota", 0)
	two := int32(2)
	p.Spec.Limits = waasv1alpha1.PolicyLimits{
		MaxWorkspaces: &two,
		PerWorkspace:  &waasv1alpha1.PerWorkspaceCaps{Memory: qty("4Gi"), Home: qty("20Gi")},
		Aggregate:     &waasv1alpha1.AggregateCaps{CPU: qty("3"), Storage: qty("30Gi")},
	}

	base := Load{CPU: resource.MustParse("1"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("10Gi")}

	if d := CheckLimits(base, true, &image, &p, nil); d != nil {
		t.Fatalf("baseline should pass: %v", d)
	}

	cases := []struct {
		name   string
		load   Load
		known  bool
		others []Load
		reason Reason
	}{
		{"count quota", base, true, []Load{base, base}, ReasonQuotaExceeded},
		{"unknown compute with caps", base, false, nil, ReasonResourcesInvalid},
		{"below image min", Load{CPU: resource.MustParse("1"), Memory: resource.MustParse("128Mi"), Storage: resource.MustParse("1Gi")}, true, nil, ReasonResourcesInvalid},
		{"above image max cpu", Load{CPU: resource.MustParse("4"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("1Gi")}, true, nil, ReasonResourcesInvalid},
		{"home too big", Load{CPU: resource.MustParse("1"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("50Gi")}, true, nil, ReasonResourcesInvalid},
		{"aggregate cpu", Load{CPU: resource.MustParse("2"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("1Gi")}, true, []Load{{CPU: resource.MustParse("2"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("1Gi")}}, ReasonQuotaExceeded},
		{"aggregate storage", base, true, []Load{{CPU: resource.MustParse("1"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("25Gi")}}, ReasonQuotaExceeded},
	}
	for _, tc := range cases {
		d := CheckLimits(tc.load, tc.known, &image, &p, tc.others)
		if d == nil || d.Reason != tc.reason {
			t.Errorf("%s: want %s, got %v", tc.name, tc.reason, d)
		}
	}

	// Paused workspaces release compute but keep storage.
	pausedOther := Load{CPU: resource.MustParse("2"), Memory: resource.MustParse("1Gi"), Storage: resource.MustParse("10Gi"), Paused: true}
	if d := CheckLimits(base, true, &image, &p, []Load{pausedOther}); d != nil {
		t.Fatalf("paused compute must not count toward cpu aggregate: %v", d)
	}
}

func TestCheckProtocol(t *testing.T) {
	image := img("x", "r/x:1", true) // vnc only
	rdpTpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "win"},
		Spec:       waasv1alpha1.WorkspaceTemplateSpec{OS: waasv1alpha1.OSWindows},
	}
	if d := CheckProtocol(rdpTpl, &image); d == nil || d.Reason != ReasonProtocolMismatch {
		t.Fatalf("want protocol mismatch, got %v", d)
	}
	vncTpl := &waasv1alpha1.WorkspaceTemplate{Spec: waasv1alpha1.WorkspaceTemplateSpec{OS: waasv1alpha1.OSLinux}}
	if d := CheckProtocol(vncTpl, &image); d != nil {
		t.Fatalf("vnc should pass: %v", d)
	}
}
