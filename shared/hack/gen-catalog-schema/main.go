// gen-catalog-schema writes the versioned JSON Schema of the shared
// catalog.yaml wire-format (catalog) to
// catalog/schema/v1.schema.json. waas is the READER of that
// format and therefore its single source of truth: the schema is
// generated from the same Go struct the parser unmarshals, never
// written by hand, so the two cannot silently diverge. Editors
// reference the published file over HTTPS (yaml-language-server) — it
// is never fetched at runtime.
//
// Run via `make generate` (build tooling only — never imported by any
// shipped binary, so invopop/jsonschema stays out of them).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"

	"github.com/xorhub/waas/shared/catalog"
)

func main() {
	r := jsonschema.Reflector{
		// Inline definitions and forbid unknown keys so the editor
		// experience matches what the tolerant parser meaningfully
		// reads.
		ExpandedStruct: true,
		DoNotReference: false,
	}
	schema := r.Reflect(&catalog.File{})
	schema.Version = "https://json-schema.org/draft/2020-12/schema"
	schema.ID = ""
	schema.Title = "WaaS image catalog manifest (waas.xorhub.io/catalog/v1)"

	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal schema: %v\n", err)
		os.Exit(1)
	}
	out = append(out, '\n')

	dest := filepath.Join("catalog", "schema", "v1.schema.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", filepath.Dir(dest), err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", dest, err)
		os.Exit(1)
	}
}
