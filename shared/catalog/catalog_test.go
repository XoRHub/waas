package catalog

import (
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	f, err := Parse([]byte(`
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/ubuntu-xfce:1.1.0@sha256:abc
    os: linux
    app: ubuntu-xfce
    version: "1.1.0"
    icon: linux
    displayName: "Ubuntu 24.04 — XFCE"
  - image: ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:def
    os: linux
    app: firefox
    version: "1.0.0"
    icon: firefox
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Images) != 2 {
		t.Fatalf("images = %d, want 2", len(f.Images))
	}
	first := f.Images[0]
	if first.DisplayName != "Ubuntu 24.04 — XFCE" || first.App != "ubuntu-xfce" || first.Version != "1.1.0" {
		t.Errorf("first entry mismatch: %+v", first)
	}
	if f.Images[1].DisplayName != "" {
		t.Errorf("absent displayName should stay zero, got %q", f.Images[1].DisplayName)
	}
}

func TestParseOptionalFieldsAbsent(t *testing.T) {
	f, err := Parse([]byte(`
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/kasmweb/terminal:1.19.0
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e := f.Images[0]
	if e.OS != "" || e.App != "" || e.Version != "" || e.Icon != "" || e.DisplayName != "" {
		t.Errorf("optional fields should be zero values: %+v", e)
	}
}

func TestParseUnknownAPIVersion(t *testing.T) {
	_, err := Parse([]byte("apiVersion: waas.xorhub.io/catalog/v2\nimages: []\n"))
	if err == nil || !strings.Contains(err.Error(), "unsupported catalog apiVersion") {
		t.Fatalf("want unsupported-apiVersion error, got %v", err)
	}
}

func TestParseMalformedYAML(t *testing.T) {
	_, err := Parse([]byte("{images: ["))
	if err == nil {
		t.Fatal("want parse error on malformed YAML")
	}
}
