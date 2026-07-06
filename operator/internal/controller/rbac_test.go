package controller

// Guards the RBAC chain end to end. The reconcilers run under the Helm
// chart's ClusterRole, which hand-mirrors the generated
// config/rbac/role.yaml — the fake-client tests exercise none of it, so a
// missing verb (e.g. `update` on deployments, which silently broke
// pause/resume scaling) is only ever caught here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func loadGeneratedRole(t *testing.T) *rbacv1.ClusterRole {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("reading generated role: %v", err)
	}
	role := &rbacv1.ClusterRole{}
	if err := yaml.Unmarshal(raw, role); err != nil {
		t.Fatalf("parsing generated role: %v", err)
	}
	return role
}

// loadChartOperatorRole extracts the operator ClusterRole from the Helm
// template. Lines carrying template directives ({{ ... }}) only occur in
// metadata, never in rules, so dropping them leaves parseable YAML.
func loadChartOperatorRole(t *testing.T) *rbacv1.ClusterRole {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "helm", "waas", "templates", "operator.yaml"))
	if err != nil {
		t.Fatalf("reading chart operator.yaml: %v", err)
	}
	for _, doc := range strings.Split(string(raw), "\n---") {
		if !strings.Contains(doc, "kind: ClusterRole\n") {
			continue
		}
		var kept []string
		for _, line := range strings.Split(doc, "\n") {
			if strings.Contains(line, "{{") {
				continue
			}
			kept = append(kept, line)
		}
		role := &rbacv1.ClusterRole{}
		if err := yaml.Unmarshal([]byte(strings.Join(kept, "\n")), role); err != nil {
			t.Fatalf("parsing chart ClusterRole: %v", err)
		}
		return role
	}
	t.Fatal("no ClusterRole found in helm/waas/templates/operator.yaml")
	return nil
}

func allows(rules []rbacv1.PolicyRule, group, resource, verb string) bool {
	for _, rule := range rules {
		groupOK := false
		for _, g := range rule.APIGroups {
			if g == group || g == rbacv1.APIGroupAll {
				groupOK = true
			}
		}
		resourceOK := false
		for _, res := range rule.Resources {
			if res == resource || res == rbacv1.ResourceAll {
				resourceOK = true
			}
		}
		verbOK := false
		for _, v := range rule.Verbs {
			if v == verb || v == rbacv1.VerbAll {
				verbOK = true
			}
		}
		if groupOK && resourceOK && verbOK {
			return true
		}
	}
	return false
}

// Every permission the kubebuilder markers declare (= what the code
// needs) must be granted by the chart's ClusterRole. Regenerate with
// `make manifests` after touching a marker, then mirror the chart.
func TestHelmChartCoversGeneratedRBAC(t *testing.T) {
	generated := loadGeneratedRole(t)
	chart := loadChartOperatorRole(t)

	for _, rule := range generated.Rules {
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				for _, verb := range rule.Verbs {
					if !allows(chart.Rules, group, resource, verb) {
						t.Errorf("chart ClusterRole is missing %s %s.%s (declared by a kubebuilder:rbac marker)",
							verb, resource, group)
					}
				}
			}
		}
	}
}

// Pause/resume and the schedule crons scale workloads in place: `update`
// on the workload kinds is load-bearing for the whole lifecycle feature.
func TestRBACAllowsWorkloadScaling(t *testing.T) {
	generated := loadGeneratedRole(t)
	for _, want := range []struct{ group, resource string }{
		{"apps", "deployments"},
		{"apps", "statefulsets"},
		{"kubevirt.io", "virtualmachines"},
	} {
		if !allows(generated.Rules, want.group, want.resource, "update") {
			t.Errorf("generated role must allow update on %s.%s (pause/resume scaling)", want.resource, want.group)
		}
	}
}
