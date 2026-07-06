package handler

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/xorhub/waas/api-server/internal/service"
)

// The scaffold must expose every top-level field of the PUT payload, so
// admins discover the whole schema in the editor.
func TestScaffoldCoversAllFields(t *testing.T) {
	for kind, typ := range map[string]reflect.Type{
		"workspacepolicy": reflect.TypeOf(service.UpsertPolicyInput{}),
		"workspaceimage":  reflect.TypeOf(service.UpsertImageInput{}),
	} {
		node := scaffoldNode(typ, 0)
		raw, err := yaml.Marshal(node)
		if err != nil {
			t.Fatalf("%s: marshal: %v", kind, err)
		}
		var got map[string]any
		if err := yaml.Unmarshal(raw, &got); err != nil {
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
