package params

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// Protocols() is the SINGLE source of protocol names (see its contract
// comment). The kubebuilder Enum markers on WorkspaceProtocol.Name and
// on the WorkspaceImage Protocol type are unavoidable copies baked into
// the generated CRDs — this test keeps them in lockstep, exactly like
// TestCRDEnumMatchesRegistry does for the overridable fields. Adding
// kasmvnc required FOUR manual edits and nearly missed one; with this
// guard (and the api-server switch replaced by a lookup) a divergence
// is a red build, not a runtime surprise.
func TestCRDProtocolEnumsMatchTheRegistry(t *testing.T) {
	want := append([]string(nil), Protocols()...)
	slices.Sort(want)

	for _, file := range []string{
		"../../config/crd/bases/waas.xorhub.io_workspacetemplates.yaml",
		"../../config/crd/bases/waas.xorhub.io_workspaceimages.yaml",
	} {
		raw, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatalf("reading %s (run `make manifests`?): %v", file, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		enums := findProtocolEnums(doc)
		if len(enums) == 0 {
			t.Fatalf("%s: no protocol enum found", file)
		}
		for _, got := range enums {
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s: CRD enum %v diverges from params.Protocols() %v — update the kubebuilder Enum markers (WorkspaceProtocol.Name and the WorkspaceImage Protocol type) and run `make manifests`",
					file, got, want)
			}
		}
	}
}

// findProtocolEnums walks a CRD schema and returns every enum list that
// looks like the protocol one (identified by containing "vnc" and "rdp"
// — stable members of the set, absent from every other enum).
func findProtocolEnums(node any) [][]string {
	var out [][]string
	switch v := node.(type) {
	case map[string]any:
		if e, ok := v["enum"].([]any); ok {
			vals := make([]string, 0, len(e))
			hasVNC, hasRDP := false, false
			for _, item := range e {
				s, _ := item.(string)
				vals = append(vals, s)
				hasVNC = hasVNC || s == "vnc"
				hasRDP = hasRDP || s == "rdp"
			}
			if hasVNC && hasRDP {
				out = append(out, vals)
			}
		}
		for _, child := range v {
			out = append(out, findProtocolEnums(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, findProtocolEnums(child)...)
		}
	}
	return out
}
