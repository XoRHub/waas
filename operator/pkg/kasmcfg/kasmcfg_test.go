package kasmcfg

import (
	"os"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func TestManagedEnvNames(t *testing.T) {
	got := ManagedEnvNames([]corev1.EnvVar{
		{Name: "START_PULSEAUDIO", Value: "1"},
		{Name: "KASM_SVC_UPLOADS", Value: "1"},
		{Name: "KASM_SVC_SMARTCARD", Value: "0"},
		{Name: "PULSE_SERVER", Value: "tcp:elsewhere"},
	})
	if want := []string{"KASM_SVC_UPLOADS", "KASM_SVC_SMARTCARD", "PULSE_SERVER"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPolicyManagedKeys(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"unrelated config", "desktop:\n  resolution:\n    width: 800\n", nil, false},
		{
			"copy enable flag",
			"data_loss_prevention:\n  clipboard:\n    server_to_client:\n      enabled: false\n",
			[]string{"data_loss_prevention.clipboard.server_to_client.enabled"},
			false,
		},
		{
			"client override flag",
			"runtime_configuration:\n  allow_client_to_override_kasm_server_settings: true\n",
			[]string{"runtime_configuration.allow_client_to_override_kasm_server_settings"},
			false,
		},
		{
			"unmanaged sub-key only",
			"data_loss_prevention:\n  clipboard:\n    server_to_client:\n      size: unlimited\n",
			nil,
			false,
		},
		{
			"all three managed keys",
			"data_loss_prevention:\n  clipboard:\n    server_to_client:\n      enabled: true\n    client_to_server:\n      enabled: true\nruntime_configuration:\n  allow_client_to_override_kasm_server_settings: true\n",
			[]string{
				"data_loss_prevention.clipboard.server_to_client.enabled",
				"data_loss_prevention.clipboard.client_to_server.enabled",
				"runtime_configuration.allow_client_to_override_kasm_server_settings",
			},
			false,
		},
		{
			"watermark text and image",
			"data_loss_prevention:\n  watermark:\n    image: /etc/kasmvnc/logo.png\n    text:\n      template: hello\n",
			[]string{"data_loss_prevention.watermark.text.template", "data_loss_prevention.watermark.image"},
			false,
		},
		{
			"unmanaged watermark sub-key only",
			"data_loss_prevention:\n  watermark:\n    text:\n      font_size: 24\n",
			nil,
			false,
		},
		{"invalid yaml", "data_loss_prevention: [unterminated\n", nil, true},
	}
	for _, tc := range cases {
		got, err := PolicyManagedKeys(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got nil", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The username is the one watermark input an admin does not type: it is
// reduced to a shell-, env- and strftime-inert charset before landing in
// a value vncserver single-quotes without escaping.
func TestWatermarkText(t *testing.T) {
	cases := []struct{ template, username, want string }{
		{"", "alice", ""},
		{"WaaS %H:%M", "alice", "WaaS %H:%M"},
		{"{user} %H:%M", "alice.smith@example.com", "alice.smith@example.com %H:%M"},
		{"{user}/{user}", "bob-1", "bob-1/bob-1"},
		{"{user}", "o'reilly", "o_reilly"},
		{"{user}", "${VNC_PW}", "__VNC_PW_"},
		{"{user}", "a %Y b", "a__Y_b"},
		{"{user}", "", ""},
		// An invalid template (admission bypassed) renders nothing.
		{"${EVIL}", "alice", ""},
	}
	for _, tc := range cases {
		if got := WatermarkText(tc.template, tc.username); got != tc.want {
			t.Errorf("WatermarkText(%q, %q) = %q, want %q", tc.template, tc.username, got, tc.want)
		}
	}
}

func TestValidateWatermark(t *testing.T) {
	for _, ok := range []string{"", "{user} %H:%M", "café ☕ " + strings.Repeat("x", WatermarkMaxLength-7)} {
		if err := ValidateWatermark(ok); err != nil {
			t.Errorf("ValidateWatermark(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"it's", "${EVIL}", "$HOME", "a\nb", "a\tb", "a\x00b", "a\x7fb", strings.Repeat("x", WatermarkMaxLength+1)} {
		if ValidateWatermark(bad) == nil {
			t.Errorf("ValidateWatermark(%q) = nil, want an error", bad)
		}
	}
}

// TestWatermarkMarkersMatchCRD keeps the api-server/controller mirror in
// lockstep with the kubebuilder markers on WorkspacePolicy.Watermark: the
// markers are comments, nothing else checks them.
func TestWatermarkMarkersMatchCRD(t *testing.T) {
	const file = "../../config/crd/bases/waas.xorhub.io_workspacepolicies.yaml"
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s (run `make manifests`?): %v", file, err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Properties struct {
									Watermark struct {
										MaxLength int    `json:"maxLength"`
										Pattern   string `json:"pattern"`
									} `json:"watermark"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil || len(crd.Spec.Versions) == 0 {
		t.Fatalf("parsing %s: %v", file, err)
	}
	w := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Watermark
	if w.MaxLength != WatermarkMaxLength || w.Pattern != watermarkPattern.String() {
		t.Fatalf("CRD watermark markers (maxLength %d, pattern %q) diverge from kasmcfg (%d, %q)",
			w.MaxLength, w.Pattern, WatermarkMaxLength, watermarkPattern.String())
	}
}
