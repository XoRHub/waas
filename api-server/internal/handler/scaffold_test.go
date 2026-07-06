package handler

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/xorhub/waas/api-server/internal/scaffold"
	"github.com/xorhub/waas/api-server/internal/service"
)

// The scaffold must expose every top-level field of the PUT payload, so
// admins discover the whole schema in the editor.
func TestScaffoldCoversAllFields(t *testing.T) {
	for kind, typ := range map[string]reflect.Type{
		"workspacepolicy": reflect.TypeOf(service.UpsertPolicyInput{}),
		"workspaceimage":  reflect.TypeOf(service.UpsertImageInput{}),
	} {
		raw, err := scaffold.YAML(typ)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		var got map[string]any
		if err := yaml.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: scaffold is not valid YAML: %v\n%s", kind, err, raw)
		}
		for i := 0; i < typ.NumField(); i++ {
			name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			if _, ok := got[name]; !ok {
				t.Fatalf("%s: scaffold missing field %q\n%s", kind, name, raw)
			}
		}
	}
}

// A scaffold must never ship placeholder ELEMENTS inside collections
// (`- kind: ""` style): collections are empty, examples live in comments.
func TestScaffoldCollectionsAreEmpty(t *testing.T) {
	raw, err := scaffold.YAML(reflect.TypeOf(service.UpsertPolicyInput{}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Subjects []any `yaml:"subjects"`
		Images   []any `yaml:"images"`
	}
	if err := yaml.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Subjects) != 0 || len(got.Images) != 0 {
		t.Fatalf("collections must be empty in a fresh scaffold:\n%s", raw)
	}
	if !strings.Contains(raw, "kind: User, name: alice") {
		t.Fatalf("the subjects example must survive as a comment:\n%s", raw)
	}
}
