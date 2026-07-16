package catalog

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
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
	if e.Profile != "" || e.Recommended != nil {
		t.Errorf("profile/recommended should be zero values: %+v", e)
	}
}

func TestParseRecommendation(t *testing.T) {
	f, err := Parse([]byte(`
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/ubuntu-xfce:1.1.0@sha256:abc
    profile: hardened
    recommended:
      podSecurityContext:
        runAsUser: 1000
        runAsNonRoot: true
      securityContext:
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
      volumes:
        - name: tmp
          mountPath: /tmp
        - name: run
          mountPath: /run
          readOnly: true
      env:
        - name: WAAS_SSH_ENABLED
          description: "Enable sshd"
          protocols: [ssh]
          default: "0"
          requires: [WAAS_SSH_AUTHORIZED_KEYS_FILE]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e := f.Images[0]
	if e.Profile != "hardened" {
		t.Errorf("profile = %q, want hardened", e.Profile)
	}
	if e.Recommended == nil {
		t.Fatal("recommended = nil, want populated")
	}
	psc := e.Recommended.PodSecurityContext
	if psc == nil || psc.RunAsUser == nil || *psc.RunAsUser != 1000 || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Errorf("podSecurityContext mismatch: %+v", psc)
	}
	sc := e.Recommended.SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Errorf("securityContext.readOnlyRootFilesystem mismatch: %+v", sc)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Errorf("securityContext.capabilities.drop mismatch: %+v", sc.Capabilities)
	}
	if len(e.Recommended.Volumes) != 2 || e.Recommended.Volumes[0] != (RecommendedVolume{Name: "tmp", MountPath: "/tmp"}) {
		t.Errorf("volumes mismatch: %+v", e.Recommended.Volumes)
	}
	if len(e.Recommended.Env) != 1 {
		t.Fatalf("env = %d entries, want 1", len(e.Recommended.Env))
	}
	env := e.Recommended.Env[0]
	if env.Name != "WAAS_SSH_ENABLED" || len(env.Protocols) != 1 || env.Protocols[0] != "ssh" || len(env.Requires) != 1 || env.Requires[0] != "WAAS_SSH_AUTHORIZED_KEYS_FILE" {
		t.Errorf("env hint mismatch: %+v", env)
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
