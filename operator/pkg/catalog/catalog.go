// Package catalog is the wire-format of the published image-catalog
// manifest (catalog.yaml) shared with the waas-images repo. It is
// deliberately DISTINCT from api/v1alpha1.DiscoveredImage: the two
// types have different compatibility cadences — this one follows an
// inter-repo file contract, the CRD type follows the v1alpha1/ADR 0002
// cycle. This single struct definition has two consumers (the catalog
// reconciler's parser and hack/gen-catalog-schema) so the published
// JSON Schema can never silently diverge from what the parser accepts.
package catalog

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// APIVersion is the only wire-format version this parser understands.
// A new version gets a new schema file (v2.schema.json) and explicit
// parser support — published versions are frozen, additive-only.
const APIVersion = "waas.xorhub.io/catalog/v1"

// File is one catalog.yaml manifest.
type File struct {
	// APIVersion must be exactly APIVersion above.
	APIVersion string `json:"apiVersion" jsonschema:"required,enum=waas.xorhub.io/catalog/v1"`
	// Images are the published entries. Optional fields left empty are
	// zero values, never an error (an empty os is treated as linux by
	// the frontend).
	Images []Entry `json:"images,omitempty"`
}

// Entry is one published image with its display metadata.
type Entry struct {
	// Image is the exact, pinned reference (digest recommended).
	Image string `json:"image" jsonschema:"required"`
	// OS is the operating-system family ("linux" or "windows"); empty
	// is treated as linux by the consumer.
	OS string `json:"os,omitempty" jsonschema:"enum=,enum=linux,enum=windows"`
	// App is a logical grouping slug (e.g. "firefox", "ubuntu-xfce").
	App string `json:"app,omitempty"`
	// Version is the human-facing version of the entry.
	Version string `json:"version,omitempty"`
	// Icon is a dashboard-icons slug (e.g. "firefox").
	Icon string `json:"icon,omitempty"`
	// DisplayName is the human-facing name; empty falls back to App.
	DisplayName string `json:"displayName,omitempty"`
}

// Parse decodes one catalog manifest. Unknown apiVersion is a clean
// error (a sync failure for the caller, never a crash); absent
// optional fields are zero values.
func Parse(data []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing catalog manifest: %w", err)
	}
	if f.APIVersion != APIVersion {
		return nil, fmt.Errorf("unsupported catalog apiVersion %q (want %q)", f.APIVersion, APIVersion)
	}
	return &f, nil
}
