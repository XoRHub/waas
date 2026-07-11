package params

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// The frontend's WorkspacePhase union (frontend/src/types.ts) is a
// hand-written copy of the kubebuilder Enum on WorkspacePhase
// (api/v1alpha1/workspace_types.go): tygo emits the field as a plain
// string, so the generated-types drift check cannot catch a phase
// renamed or added on the Go side. This test keeps the two in lockstep
// the same way TestCRDProtocolEnumsMatchTheRegistry does for protocol
// names — it lives here because pkg/params hosts the repo's
// cross-artifact enum sync guards, not because phases are params.
func TestFrontendWorkspacePhaseMatchesCRDEnum(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean("../../config/crd/bases/waas.xorhub.io_workspaces.yaml"))
	if err != nil {
		t.Fatalf("reading workspaces CRD (run `make manifests`?): %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing workspaces CRD: %v", err)
	}
	enums := findPhaseEnums(doc)
	if len(enums) == 0 {
		t.Fatal("workspaces CRD: no phase enum found")
	}
	want := append([]string(nil), enums[0]...)
	slices.Sort(want)
	for _, e := range enums[1:] {
		slices.Sort(e)
		if !slices.Equal(e, want) {
			t.Fatalf("workspaces CRD: two phase enums diverge: %v vs %v", e, want)
		}
	}

	got := frontendPhaseUnion(t, "../../../frontend/src/types.ts")
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("frontend WorkspacePhase %v diverges from the CRD phase enum %v — update the union in frontend/src/types.ts (source of truth: the kubebuilder Enum marker on WorkspacePhase in api/v1alpha1/workspace_types.go)",
			got, want)
	}
}

// frontendPhaseUnion extracts the literal union of the exported
// WorkspacePhase type with a regex — same spirit as findProtocolEnums:
// no real parser, just a stable-shape extract that fails loudly when the
// declaration moves or changes form.
func frontendPhaseUnion(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading frontend types: %v", err)
	}
	decl := regexp.MustCompile(`(?s)export type WorkspacePhase =\s*(.*?);`).FindSubmatch(raw)
	if decl == nil {
		t.Fatal("frontend/src/types.ts: no `export type WorkspacePhase = …;` declaration found")
	}
	lits := regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(decl[1], -1)
	if len(lits) == 0 {
		t.Fatal("frontend/src/types.ts: WorkspacePhase declaration has no string literals")
	}
	out := make([]string, 0, len(lits))
	for _, m := range lits {
		out = append(out, string(m[1]))
	}
	return out
}

// findPhaseEnums walks the CRD schema and returns every enum list that
// looks like the phase one (identified by containing "Pending" and
// "Running" — stable members, absent together from every other enum in
// the schema, including the embedded PVC spec ones).
func findPhaseEnums(node any) [][]string {
	var out [][]string
	switch v := node.(type) {
	case map[string]any:
		if e, ok := v["enum"].([]any); ok {
			vals := make([]string, 0, len(e))
			hasPending, hasRunning := false, false
			for _, item := range e {
				s, _ := item.(string)
				vals = append(vals, s)
				hasPending = hasPending || s == "Pending"
				hasRunning = hasRunning || s == "Running"
			}
			if hasPending && hasRunning {
				out = append(out, vals)
			}
		}
		for _, child := range v {
			out = append(out, findPhaseEnums(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, findPhaseEnums(child)...)
		}
	}
	return out
}
