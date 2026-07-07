package policy

// Registry guards: these tests are what should have caught the
// "allowedFields rejects schedule/placement/metadata" drift. Any new
// field on WorkspaceSpec or WorkspaceOverrides, any new OverridableField
// and any CRD enum change must reconcile with the single registry — or
// the build goes red and forces an explicit decision.

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// TestOverrideRegistryIsExhaustive pins spec ↔ registry both ways.
func TestOverrideRegistryIsExhaustive(t *testing.T) {
	all := waasv1alpha1.AllOverridableFields()

	// 1. Every WorkspaceOverrides field is claimed by a right.
	ovType := reflect.TypeOf(waasv1alpha1.WorkspaceOverrides{})
	for i := 0; i < ovType.NumField(); i++ {
		name := jsonName(ovType.Field(i))
		field, ok := overrideClaims[name]
		if !ok {
			t.Errorf("WorkspaceOverrides.%s (json %q) is claimed by NO override right: add it to overrideClaims (pkg/policy/overrides.go) or make it a governed/exempt spec decision", ovType.Field(i).Name, name)
			continue
		}
		if !slices.Contains(all, field) {
			t.Errorf("overrideClaims[%q] = %q is not a declared OverridableField", name, field)
		}
	}
	// ... and no claim points at a vanished struct field.
	for name := range overrideClaims {
		found := false
		for i := 0; i < ovType.NumField(); i++ {
			found = found || jsonName(ovType.Field(i)) == name
		}
		if !found {
			t.Errorf("overrideClaims[%q] refers to a field WorkspaceOverrides no longer has", name)
		}
	}

	// 2. Every WorkspaceSpec field is either governed (specClaims) or
	// explicitly exempted with a reason.
	specType := reflect.TypeOf(waasv1alpha1.WorkspaceSpec{})
	for i := 0; i < specType.NumField(); i++ {
		name := jsonName(specType.Field(i))
		_, governed := specClaims[name]
		_, exempt := specExempt[name]
		if !governed && !exempt {
			t.Errorf("WorkspaceSpec.%s (json %q) is neither governed (specClaims) nor exempted (specExempt): decide its governance explicitly in pkg/policy/overrides.go", specType.Field(i).Name, name)
		}
		if governed && exempt {
			t.Errorf("WorkspaceSpec %q is both governed and exempt — pick one", name)
		}
	}

	// 3. Every declared right is consumed somewhere: by an overrides
	// field, a spec field, or a documented connect-time enforcement.
	for _, field := range all {
		consumed := false
		for _, f := range overrideClaims {
			consumed = consumed || f == field
		}
		for _, f := range specClaims {
			consumed = consumed || f == field
		}
		if _, ok := connectTimeRights[field]; ok {
			consumed = true
		}
		if !consumed {
			t.Errorf("OverridableField %q is enforced NOWHERE: claim it in overrideClaims/specClaims or register its out-of-spec enforcement in connectTimeRights", field)
		}
	}

	// 4. Every right documents itself (the meta API serves this to the
	// admin editors).
	desc := waasv1alpha1.OverridableFieldDescriptions()
	for _, field := range all {
		if desc[field] == "" {
			t.Errorf("OverridableField %q has no description (OverridableFieldDescriptions)", field)
		}
	}
}

// TestCRDEnumMatchesRegistry keeps the kubebuilder Enum marker (and thus
// the generated CRD schemas GitOps validates against) in lockstep with
// AllOverridableFields: the marker is a comment, nothing else checks it.
func TestCRDEnumMatchesRegistry(t *testing.T) {
	want := make([]string, 0)
	for _, f := range waasv1alpha1.AllOverridableFields() {
		want = append(want, string(f))
	}
	slices.Sort(want)

	for _, file := range []string{
		"../../config/crd/bases/waas.xorhub.io_workspacetemplates.yaml",
		"../../config/crd/bases/waas.xorhub.io_workspacepolicies.yaml",
	} {
		raw, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatalf("reading %s (run `make manifests`?): %v", file, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		enums := findOverrideEnums(doc)
		if len(enums) == 0 {
			t.Fatalf("%s: no allowedFields enum found", file)
		}
		for _, got := range enums {
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s: CRD enum %v diverges from AllOverridableFields %v — update the kubebuilder Enum marker on OverridableField and run `make manifests`", file, got, want)
			}
		}
	}
}

// findOverrideEnums walks the CRD schema and returns every enum list that
// looks like the OverridableField one (identified by containing "env" and
// "protocolParams" — stable members of the set).
func findOverrideEnums(node any) [][]string {
	var out [][]string
	switch v := node.(type) {
	case map[string]any:
		if e, ok := v["enum"].([]any); ok {
			vals := make([]string, 0, len(e))
			hasEnv, hasPP := false, false
			for _, item := range e {
				s, _ := item.(string)
				vals = append(vals, s)
				hasEnv = hasEnv || s == "env"
				hasPP = hasPP || s == "protocolParams"
			}
			if hasEnv && hasPP {
				out = append(out, vals)
			}
		}
		for _, child := range v {
			out = append(out, findOverrideEnums(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, findOverrideEnums(child)...)
		}
	}
	return out
}

// TestCheckOverridesEnforcementMatrix exercises EVERY spec-borne right:
// allowed by template+policy → accepted; missing from the policy →
// denied. protocolParams is connect-time (api-server tests cover it).
func TestCheckOverridesEnforcementMatrix(t *testing.T) {
	usages := map[waasv1alpha1.OverridableField]func(ws *waasv1alpha1.Workspace){
		waasv1alpha1.FieldEnv: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Env: []corev1.EnvVar{{Name: "FOO", Value: "1"}}}
		},
		waasv1alpha1.FieldSecurityContext: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{SecurityContext: &corev1.SecurityContext{}}
		},
		waasv1alpha1.FieldPodSecurityContext: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{PodSecurityContext: &corev1.PodSecurityContext{}}
		},
		waasv1alpha1.FieldVolumes: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{VolumeMounts: []corev1.VolumeMount{{Name: "x", MountPath: "/x"}}}
		},
		waasv1alpha1.FieldNodeSelector: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{NodeSelector: map[string]string{"zone": "a"}}
		},
		waasv1alpha1.FieldTolerations: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Tolerations: []corev1.Toleration{{Key: "k"}}}
		},
		waasv1alpha1.FieldProtocol: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Protocol: "rdp"}
		},
		waasv1alpha1.FieldSchedule: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Schedule: &waasv1alpha1.WorkspaceSchedule{
				Timezone: "Europe/Paris", Downtime: []string{"0 20 * * *"},
			}}
		},
		waasv1alpha1.FieldMetadata: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Labels: map[string]string{"team": "cad"}}
		},
		waasv1alpha1.FieldResources: func(ws *waasv1alpha1.Workspace) {
			// Presence is the override — even values copied from the
			// template consume the right (validated design).
			ws.Spec.Resources = &corev1.ResourceRequirements{}
		},
		waasv1alpha1.FieldPlacement: func(ws *waasv1alpha1.Workspace) {
			ws.Spec.TargetNamespace = "waas-bob-custom" // deviates from the built-in default
		},
	}

	// Sanity: the matrix covers every spec-borne right.
	for _, f := range waasv1alpha1.AllOverridableFields() {
		if _, connectTime := connectTimeRights[f]; connectTime {
			continue
		}
		if _, ok := usages[f]; !ok {
			t.Fatalf("enforcement matrix misses right %q — add a usage builder", f)
		}
	}

	bob := Identity{Owner: "u1", Username: "bob"}
	for field, use := range usages {
		t.Run(string(field), func(t *testing.T) {
			tpl := &waasv1alpha1.WorkspaceTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "xfce"},
				Spec: waasv1alpha1.WorkspaceTemplateSpec{
					OS: waasv1alpha1.OSLinux,
					Protocols: []waasv1alpha1.WorkspaceProtocol{
						{Name: "vnc", Port: 5901, Default: true},
						{Name: "rdp", Port: 3389},
					},
					Overrides: &waasv1alpha1.TemplateOverrides{AllowedFields: waasv1alpha1.AllOverridableFields()},
				},
			}
			ws := &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
			use(ws)

			allowing := &waasv1alpha1.WorkspacePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "allowing"},
				Spec: waasv1alpha1.WorkspacePolicySpec{
					Overrides: &waasv1alpha1.PolicyOverrides{AllowedFields: []waasv1alpha1.OverridableField{field}},
				},
			}
			if d := CheckOverrides(ws, tpl, allowing, bob, ""); d != nil {
				t.Fatalf("%s allowed by template+policy must pass: %v", field, d)
			}

			// Policy grants every OTHER right: this field must be denied.
			var others []waasv1alpha1.OverridableField
			for _, f := range waasv1alpha1.AllOverridableFields() {
				if f != field {
					others = append(others, f)
				}
			}
			denying := &waasv1alpha1.WorkspacePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "denying"},
				Spec: waasv1alpha1.WorkspacePolicySpec{
					Overrides: &waasv1alpha1.PolicyOverrides{AllowedFields: others},
				},
			}
			if d := CheckOverrides(ws, tpl, denying, bob, ""); d == nil || d.Reason != ReasonOverrideNotAllowed {
				t.Fatalf("%s absent from the policy must be denied, got %v", field, d)
			}

			// Template does not allow the field either (and no policy):
			// fail-closed at the template level.
			tpl.Spec.Overrides = nil
			if d := CheckOverrides(ws, tpl, nil, bob, ""); d == nil || d.Reason != ReasonOverrideNotAllowed {
				t.Fatalf("%s absent from the template must be denied (fail-closed), got %v", field, d)
			}
		})
	}
}
