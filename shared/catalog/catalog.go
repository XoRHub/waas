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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// APIVersion is the only wire-format version this parser understands.
// A new version gets a new schema file (v2.schema.json) and explicit
// parser support — published versions are frozen, additive-only.
const APIVersion = "waas.xorhub.io/catalog/v1"

// ProfileHardened and ProfileNormal are the only non-empty Entry.Profile
// values a consumer keeps; anything else is degraded to "" at the sync
// boundary (see CatalogSyncWorker's normalizeProfile), the same
// enum-safe treatment OS gets.
const (
	ProfileHardened = "hardened"
	ProfileNormal   = "normal"
)

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
	// Icon is an icon reference: an absolute https URL, a
	// `file:<path>` path internal to the frontend, or a
	// dashboard-icons slug (e.g. "firefox") — see
	// docs/image-catalog.md.
	Icon string `json:"icon,omitempty"`
	// DisplayName is the human-facing name; empty falls back to App.
	DisplayName string `json:"displayName,omitempty"`
	// Profile is a display badge ("hardened" or "normal"); empty means
	// no badge is shown. Purely cosmetic — never read by enforce() or
	// buildPodTemplate.
	Profile string `json:"profile,omitempty" jsonschema:"enum=,enum=hardened,enum=normal"`
	// Recommended is an optional deployment-prefill hint for the admin
	// template form — never merged into a built pod, never validated by
	// a webhook. See docs/image-catalog.md.
	Recommended *Recommendation `json:"recommended,omitempty"`
}

// Recommendation is a display/prefill hint attached to one catalog
// Entry: what the admin template form can offer to copy into a
// WorkspaceTemplate's Workload on explicit request. Never read at
// reconcile time.
type Recommendation struct {
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	SecurityContext    *corev1.SecurityContext    `json:"securityContext,omitempty"`
	Volumes            []RecommendedVolume        `json:"volumes,omitempty"`
	Env                []EnvHint                  `json:"env,omitempty"`
}

// RecommendedVolume is deliberately NOT a corev1.Volume+corev1.VolumeMount
// pair by name: the only case HARDENING.md actually documents is a
// plain emptyDir mounted at a fixed path (the /tmp+/run pair needed
// when readOnlyRootFilesystem is recommended) — one name+mountPath
// entry says that without repeating the volume/mount boilerplate twice
// per entry, and removes any risk of an unpaired volume/mount by
// construction. Never covers configMap/secret-backed mounts (e.g. an
// init.d script volume): those stay entirely the admin's call via the
// free-form WorkspaceWorkload.Volumes/VolumeMounts override at
// template-edit time, never suggested by the catalog.
type RecommendedVolume struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// EnvHint is a display/prefill hint for one recommended env var. No
// Kind field, deliberately: an EnvVar's value is always a string
// wire-side (corev1.EnvVar.Value) regardless of what it "means" — a
// bool/int/enum classification would never drive a different widget
// in this feature, so it stays out. The one real structural
// distinction that exists — literal Value vs ValueFrom.SecretKeyRef —
// is left to Description text rather than a new field.
type EnvHint struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Protocols this hint applies to; empty means all protocols.
	Protocols []string `json:"protocols,omitempty" jsonschema:"enum=vnc,enum=rdp,enum=ssh,enum=kasmvnc"`
	// Requires names other Env[].Name entries of the SAME recommendation
	// that make no sense without this one (e.g. WAAS_SSH_ENABLED
	// requires WAAS_SSH_AUTHORIZED_KEYS_FILE). Purely descriptive: lets
	// the prefill UI group/warn together, never validated, never
	// enforced.
	Requires []string `json:"requires,omitempty"`
	Default  string   `json:"default,omitempty"`
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
