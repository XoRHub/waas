package policy

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func exactEntry(name, image string) waasv1alpha1.WorkspaceImage {
	return waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       waasv1alpha1.WorkspaceImageSpec{Image: image, Enabled: true},
	}
}

func registryEntry(name, registry string, tagPolicy waasv1alpha1.ImageTagPolicy) waasv1alpha1.WorkspaceImage {
	return waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       waasv1alpha1.WorkspaceImageSpec{Registry: registry, TagPolicy: tagPolicy, Enabled: true},
	}
}

func TestFindImageRegistryMatching(t *testing.T) {
	catalog := []waasv1alpha1.WorkspaceImage{
		exactEntry("terminal-pinned", "docker.io/kasmweb/terminal:1.19.0"),
		registryEntry("kasmweb", "docker.io/kasmweb", ""),
		registryEntry("dockerhub", "docker.io", ""),
	}

	cases := []struct {
		ref  string
		want string // entry name, "" = no match
	}{
		// Exact entry beats every registry entry.
		{"docker.io/kasmweb/terminal:1.19.0", "terminal-pinned"},
		// Longest registry prefix wins.
		{"docker.io/kasmweb/firefox:1.19.0", "kasmweb"},
		{"docker.io/library/nginx:1.27.0", "dockerhub"},
		// Path-boundary: a prefix must not bleed into sibling namespaces.
		{"docker.io/kasmweb-evil/x:1.0", "dockerhub"},
		{"ghcr.io/xorhub/waas/operator:v1.0.0", ""},
	}
	for _, tc := range cases {
		got := FindImage(catalog, tc.ref)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: expected no match, got %q", tc.ref, got.Name)
		case tc.want != "" && (got == nil || got.Name != tc.want):
			t.Errorf("%s: expected entry %q, got %v", tc.ref, tc.want, got)
		}
	}

	// A registry entry with a trailing slash behaves identically.
	slashed := []waasv1alpha1.WorkspaceImage{registryEntry("k", "docker.io/kasmweb/", "")}
	if got := FindImage(slashed, "docker.io/kasmweb/terminal:1.19.0"); got == nil {
		t.Error("trailing-slash registry prefix must still match")
	}
}

func TestCheckTagDiscipline(t *testing.T) {
	cases := []struct {
		name    string
		policy  waasv1alpha1.ImageTagPolicy
		ref     string
		wantErr string
	}{
		// Default ("") is "tag": fixed tags and digests pass…
		{"default fixed tag", "", "r/x:1.2.3", ""},
		{"default digest", "", "r/x@sha256:abc", ""},
		{"default tag+digest", "", "r/x:1.2.3@sha256:abc", ""},
		// …moving references do not.
		{"default latest", "", "r/x:latest", "moving reference"},
		{"default tagless", "", "r/x", "moving reference"},
		// Registry ports are not tags.
		{"port no tag", "", "host:5000/r/x", "moving reference"},
		{"port with tag", "", "host:5000/r/x:1.0", ""},
		// digest: only @sha256 passes.
		{"digest ok", waasv1alpha1.TagPolicyDigest, "r/x:1.2.3@sha256:abc", ""},
		{"digest missing", waasv1alpha1.TagPolicyDigest, "r/x:1.2.3", "digest-pinned"},
		// any: everything, latest included.
		{"any latest", waasv1alpha1.TagPolicyAny, "r/x:latest", ""},
		{"any tagless", waasv1alpha1.TagPolicyAny, "r/x", ""},
	}
	// Exact entries default to "any": the approval is verbatim — an
	// admin who wrote ":latest" in the entry said so explicitly. An
	// explicit tagPolicy on an exact entry still bites.
	exact := exactEntry("verbatim", "r/x:latest")
	if d := CheckTagDiscipline(&exact, "r/x:latest"); d != nil {
		t.Errorf("exact entry default must be 'any': %s", d.Message)
	}
	exact.Spec.TagPolicy = waasv1alpha1.TagPolicyTag
	if d := CheckTagDiscipline(&exact, "r/x:latest"); d == nil {
		t.Error("explicit tagPolicy on an exact entry must be enforced")
	}

	for _, tc := range cases {
		img := registryEntry("e", "r", tc.policy)
		d := CheckTagDiscipline(&img, tc.ref)
		if tc.wantErr == "" && d != nil {
			t.Errorf("%s: unexpected denial: %s", tc.name, d.Message)
		}
		if tc.wantErr != "" {
			if d == nil {
				t.Errorf("%s: expected a denial", tc.name)
			} else if d.Reason != ReasonImageTagPolicy || !strings.Contains(d.Message, tc.wantErr) {
				t.Errorf("%s: expected ImageTagPolicy %q, got %s %q", tc.name, tc.wantErr, d.Reason, d.Message)
			}
		}
	}
}
