package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Slices and maps in API models MUST carry omitempty: a nil Go slice
// serialized as null where the frontend types promise an array is the
// exact root cause of the "params is null" crash. The generated
// TypeScript (types.gen.ts) emits arrays, never nullables — this guard
// keeps that promise true. (Nil maps/slices with omitempty are OMITTED,
// and the frontend's `?? []` defaults handle absence honestly.)
func TestModelSlicesAndMapsCarryOmitempty(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				switch field.Type.(type) {
				case *ast.ArrayType, *ast.MapType:
				default:
					continue
				}
				if field.Tag == nil {
					continue
				}
				// Escape hatch for fields where the EMPTY list is
				// semantic or consumers iterate unguarded: the doc
				// comment must say so and the projection must keep the
				// promise ("non-nil guaranteed").
				if field.Doc != nil && strings.Contains(field.Doc.Text(), "non-nil guaranteed") {
					continue
				}
				tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json")
				if tag == "" || tag == "-" {
					continue
				}
				if !strings.Contains(tag, "omitempty") {
					pos := fset.Position(field.Pos())
					violations = append(violations, pos.String()+" json:\""+tag+"\"")
				}
			}
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("slice/map model fields without omitempty (they serialize null and lie to the generated frontend types):\n  %s",
			strings.Join(violations, "\n  "))
	}
}
