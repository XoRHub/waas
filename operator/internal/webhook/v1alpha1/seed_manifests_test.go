package v1alpha1

// Guards the seed manifests: every dev template (hack/dev) must pass the
// REAL template webhook, and the governance seeds must only use known
// overridable fields. Broken seeds have already shipped once (the SSH
// image missing from the default policy made SSH templates invisible);
// this test makes the seeds part of the validated surface.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func readDocs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var docs []string
	for _, doc := range strings.Split(string(raw), "\n---") {
		if strings.TrimSpace(doc) != "" {
			docs = append(docs, doc)
		}
	}
	return docs
}

func TestDevTemplateSeedsPassTheWebhook(t *testing.T) {
	v := &WorkspaceTemplateValidator{}
	for _, doc := range readDocs(t, "hack/dev/templates-dev.yaml") {
		tpl := &waasv1alpha1.WorkspaceTemplate{}
		if err := sigsyaml.UnmarshalStrict([]byte(doc), tpl); err != nil {
			t.Fatalf("hack/dev/templates-dev.yaml: %v\n%s", err, doc)
		}
		if _, err := v.ValidateCreate(context.Background(), tpl); err != nil {
			t.Errorf("template %q must pass the webhook: %v", tpl.Name, err)
		}
		if sched := tpl.Spec.Schedule; sched != nil && sched.Timezone == "" {
			t.Errorf("template %q: schedule without timezone", tpl.Name)
		}
	}
}

func TestGovernancePolicySeedsUseKnownFields(t *testing.T) {
	for _, doc := range readDocs(t, "gitops/governance/policies.yaml") {
		pol := &waasv1alpha1.WorkspacePolicy{}
		if err := sigsyaml.UnmarshalStrict([]byte(doc), pol); err != nil {
			t.Fatalf("gitops/governance/policies.yaml: %v\n%s", err, doc)
		}
		if pol.Spec.Overrides == nil {
			continue
		}
		for _, f := range pol.Spec.Overrides.AllowedFields {
			if !slices.Contains(waasv1alpha1.AllOverridableFields(), f) {
				t.Errorf("policy %q: unknown overridable field %q", pol.Name, f)
			}
		}
	}
}
