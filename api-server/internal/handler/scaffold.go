package handler

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/service"
)

// scaffoldTypes maps a scaffold kind to the exact PUT payload type the
// governance editors submit. Deriving the skeleton from these Go types
// (the same types the CRDs are generated from) means the scaffold can
// never drift from the API — no hand-maintained template.
var scaffoldTypes = map[string]reflect.Type{
	"workspacepolicy": reflect.TypeOf(service.UpsertPolicyInput{}),
	"workspaceimage":  reflect.TypeOf(service.UpsertImageInput{}),
}

// Scaffold handles GET /api/v1/meta/scaffold/{kind}: a YAML skeleton with
// EVERY field of the object, so admins discover the whole schema in the
// editor instead of going back to the docs.
func (h *MetaHandler) Scaffold(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	t, known := scaffoldTypes[kind]
	if !known {
		fail(w, r, apierror.NotFound("unknown scaffold kind "+kind))
		return
	}
	node := scaffoldNode(t, 0)
	out, err := yaml.Marshal(node)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	ok(w, map[string]string{"scaffold": string(out)})
}

// scaffoldNode builds an ordered YAML node tree with a placeholder value
// for every field, in struct-declaration order.
func scaffoldNode(t reflect.Type, depth int) *yaml.Node {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if depth > 8 {
		return scalar("null", "!!null")
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
			n.Content = append(n.Content, scalar(name, ""), scaffoldNode(f.Type, depth+1))
		}
		return n
	case reflect.Slice, reflect.Array:
		// One example element reveals the element shape.
		return &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{scaffoldNode(t.Elem(), depth+1)}}
	case reflect.Map:
		// Dynamic keys: show an empty mapping (the field name documents it).
		return &yaml.Node{Kind: yaml.MappingNode}
	case reflect.String:
		return scalar("", "!!str")
	case reflect.Bool:
		return scalar("false", "!!bool")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return scalar("0", "!!int")
	case reflect.Float32, reflect.Float64:
		return scalar("0", "!!float")
	default:
		return scalar("null", "!!null")
	}
}

func scalar(value, tag string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag}
}
