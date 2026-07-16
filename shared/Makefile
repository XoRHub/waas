# WaaS shared module — codegen.
# Never edit catalog/schema/v1.schema.json by hand: run `make generate`
# after any change under catalog/.

.PHONY: generate

generate:
	# JSON Schema of the shared catalog.yaml wire-format, generated from
	# ./catalog so it can never diverge from the parser. Drift-gated by
	# the go-generated-drift CI job (its diff now also covers shared/).
	go run ./hack/gen-catalog-schema
