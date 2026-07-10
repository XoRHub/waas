package service

import (
	"strings"
	"testing"
)

// specFromInput mirrors the admission webhook's exposeAudioPort gates
// with 400s: vnc-only, and no protocol may squat the PulseAudio port.
func TestTemplateInputValidatesExposeAudioPort(t *testing.T) {
	base := func(protocols ...TemplateProtocolInput) TemplateInput {
		return TemplateInput{
			Name: "t", DisplayName: "T", OS: "linux", Image: "img:1",
			Protocols: protocols,
		}
	}

	cases := []struct {
		name    string
		in      TemplateInput
		wantErr string
	}{
		{
			"audio port on vnc",
			base(TemplateProtocolInput{Name: "vnc", Port: 5901, ExposeAudioPort: true}),
			"",
		},
		{
			"audio port on ssh",
			base(TemplateProtocolInput{Name: "ssh", Port: 22, ExposeAudioPort: true}),
			"only the vnc protocol",
		},
		{
			"audio port colliding with a protocol port",
			base(
				TemplateProtocolInput{Name: "vnc", Port: 5901, ExposeAudioPort: true},
				TemplateProtocolInput{Name: "rdp", Port: 4713},
			),
			"collides with the exposed PulseAudio port",
		},
	}
	for _, tc := range cases {
		spec, err := specFromInput(tc.in)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			} else if !spec.Protocols[0].ExposeAudioPort {
				t.Errorf("%s: exposeAudioPort must reach the CR spec", tc.name)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

// specFromInput mirrors the admission webhook's kasmvncConfig gates with
// 400s (the api-server validates before submitting to k8s so the admin
// sees the reason, not a wrapped admission 500).
func TestTemplateInputValidatesKasmVNCConfig(t *testing.T) {
	base := func(config string, protocols ...TemplateProtocolInput) TemplateInput {
		return TemplateInput{
			Name: "t", DisplayName: "T", OS: "linux", Image: "img:1",
			KasmVNCConfig: config,
			Protocols:     protocols,
		}
	}
	kasm := TemplateProtocolInput{Name: "kasmvnc", Port: 6901, Default: true}
	vnc := TemplateProtocolInput{Name: "vnc", Port: 5901, Default: true}

	cases := []struct {
		name    string
		in      TemplateInput
		wantErr string
	}{
		{"benign config with kasmvnc", base("desktop:\n  resolution:\n    width: 800\n", kasm), ""},
		{"empty config", base("", kasm), ""},
		{"config without kasmvnc protocol", base("desktop: {}", vnc), "requires a kasmvnc protocol"},
		{
			"config setting a policy-managed clipboard key",
			base("data_loss_prevention:\n  clipboard:\n    server_to_client:\n      enabled: true\n", kasm),
			"clipboard enforcement is derived from WorkspacePolicy.Clipboard",
		},
		{
			"config with the client-override flag",
			base("runtime_configuration:\n  allow_client_to_override_kasm_server_settings: true\n", kasm),
			"clipboard enforcement is derived from WorkspacePolicy.Clipboard",
		},
		{"config with an unmanaged clipboard sub-key", base("data_loss_prevention:\n  clipboard:\n    server_to_client:\n      size: unlimited\n", kasm), ""},
		{"invalid YAML", base("data_loss_prevention: [unterminated\n", kasm), "not valid YAML"},
	}
	for _, tc := range cases {
		_, err := specFromInput(tc.in)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}
