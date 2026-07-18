// Package scaffold renders the YAML skeletons of the governance editors
// (policies, image catalog). The skeleton structure is derived by
// reflection from the exact PUT payload types, so it can never drift from
// the API; the hint table only layers curated example VALUES (where the
// zero value would not survive validation) and explanatory comments.
//
// Contract, enforced by test: a freshly generated scaffold passes the
// corresponding upsert validation unmodified. That means collections stay
// EMPTY (`[]`, with the example shape in a comment) instead of carrying
// invalid placeholder elements like `- kind: ""`.
package scaffold

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// hint tunes one field of the generic skeleton, keyed "<Type>.<jsonName>".
type hint struct {
	// value replaces the generic zero value (used when validation
	// requires something non-empty, e.g. an image reference).
	value *yaml.Node
	// comment is rendered at the end of the field's line.
	comment string
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!str"} }
func boolean(v bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%t", v), Tag: "!!bool"}
}
func integer(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", v), Tag: "!!int"}
}
func seq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: items}
}

func overridableFields() string {
	names := make([]string, 0, len(waasv1alpha1.AllOverridableFields()))
	for _, f := range waasv1alpha1.AllOverridableFields() {
		names = append(names, string(f))
	}
	return strings.Join(names, " | ")
}

var hints = map[string]hint{
	// WorkspacePolicy editor.
	"UpsertPolicyInput.priority":             {comment: "higher wins; 0 = default fallback"},
	"UpsertPolicyInput.subjects":             {comment: `[] = every authenticated user; e.g. [{kind: User, name: alice}] — kind: User | Group`},
	"UpsertPolicyInput.images":               {comment: "catalog entry names; [] = the whole enabled catalog"},
	"UpsertPolicyInput.lifecycle":            {comment: `Go durations; e.g. {idleSuspendAfter: 30m, maxLifetime: 720h}`},
	"PolicyLimitsModel.maxWorkspaces":        {value: integer(3), comment: "simultaneous workspaces per user (paused included)"},
	"PolicyLimitsModel.maxRunningWorkspaces": {comment: "running (compute active) workspaces per user; paused excluded"},
	"PolicyLimitsModel.perWorkspace":         {comment: `e.g. {cpu: "2", memory: 4Gi, home: 20Gi}`},
	"PolicyLimitsModel.aggregate":            {comment: `sum across the user's workspaces; e.g. {cpu: "8", memory: 32Gi, storage: 100Gi}`},
	"PolicyLimitsModel.defaults":             {comment: `portal pre-selection (display-only); e.g. {cpu: "2", memory: 4Gi}`},
	"ClipboardPolicyModel.copyFromWorkspace": {value: boolean(true), comment: "workspace → local clipboard"},
	"ClipboardPolicyModel.pasteToWorkspace":  {value: boolean(true), comment: "local clipboard → workspace"},
	"PolicyOverridesModel.allowedFields":     {comment: "among: " + overridableFields() + "; [] forbids every override"},

	// WorkspaceImage (catalog) editor. displayName/image/protocols are
	// required by validation, so they carry valid example values.
	"UpsertImageInput.displayName":   {value: str("Example desktop")},
	"UpsertImageInput.image":         {value: str("registry.example.com/desktop:1.0.0")},
	"UpsertImageInput.protocols":     {value: seq(str("vnc")), comment: "vnc | rdp | ssh"},
	"UpsertImageInput.architectures": {comment: "e.g. [amd64, arm64]; [] = no scheduling constraint"},
	"UpsertImageInput.enabled":       {value: boolean(true)},
	"UpsertImageInput.allowedGroups": {comment: "IdP (OIDC) groups; [] = everyone"},
	"UpsertImageInput.defaults":      {comment: `e.g. {cpu: "2", memory: 4Gi}`},
	"UpsertImageInput.min":           {comment: `e.g. {cpu: "1", memory: 2Gi}`},
	"UpsertImageInput.max":           {comment: `e.g. {cpu: "4", memory: 16Gi}`},
}

// YAML renders the scaffold of the given payload type.
func YAML(t reflect.Type) (string, error) {
	out, err := yaml.Marshal(node(t, 0))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// node builds an ordered YAML node tree, in struct-declaration order.
// Every generated value is VALID: empty collections stay empty (hints
// document the element shape), scalars get their zero value unless a hint
// supplies a better one.
func node(t reflect.Type, depth int) *yaml.Node {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if depth > 8 {
		return &yaml.Node{Kind: yaml.ScalarNode, Value: "null", Tag: "!!null"}
	}
	switch t.Kind() {
	case reflect.Struct:
		n := &yaml.Node{Kind: yaml.MappingNode}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			value := node(f.Type, depth+1)
			if h, found := hints[t.Name()+"."+name]; found {
				if h.value != nil {
					value = h.value
				}
				value.LineComment = h.comment
			}
			n.Content = append(n.Content, str(name), value)
		}
		return n
	case reflect.Slice, reflect.Array:
		return seq() // [] — never a placeholder element
	case reflect.Map:
		return &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle} // {}
	case reflect.String:
		return str("")
	case reflect.Bool:
		return boolean(false)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return integer(0)
	case reflect.Float32, reflect.Float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: "0", Tag: "!!float"}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: "null", Tag: "!!null"}
	}
}
