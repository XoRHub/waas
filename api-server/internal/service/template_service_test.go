package service

import (
	"strings"
	"testing"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
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

// specFromInput mirrors the admission webhook's structural protocol
// gates with 400s: no duplicate declaration, and kasmvnc is exclusive
// (it bypasses guacd and must be the template's only protocol).
func TestTemplateInputValidatesProtocolCombinations(t *testing.T) {
	base := func(protocols ...TemplateProtocolInput) TemplateInput {
		return TemplateInput{
			Name: "t", DisplayName: "T", OS: "linux", Image: "img:1",
			Protocols: protocols,
		}
	}
	kasm := TemplateProtocolInput{Name: "kasmvnc", Port: 6901}
	vnc := TemplateProtocolInput{Name: "vnc", Port: 5901}
	rdp := TemplateProtocolInput{Name: "rdp", Port: 3389}
	ssh := TemplateProtocolInput{Name: "ssh", Port: 22}

	cases := []struct {
		name    string
		in      TemplateInput
		wantErr string
	}{
		{"kasmvnc alone", base(kasm), ""},
		{"vnc+rdp+ssh without kasmvnc", base(vnc, rdp, ssh), ""},
		{"kasmvnc with vnc", base(kasm, vnc), "cannot be combined"},
		{"kasmvnc with rdp", base(kasm, rdp), "cannot be combined"},
		{"kasmvnc with ssh", base(kasm, ssh), "cannot be combined"},
		{"kasmvnc with vnc+rdp+ssh", base(kasm, vnc, rdp, ssh), "cannot be combined"},
		{"protocol declared twice", base(vnc, vnc), "declared twice"},
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

// specFromInput mirrors the admission webhook's metadata denylist on the
// homeVolume block with 400s, and passes clean metadata to the CR spec
// verbatim. Create and Update both funnel through specFromInput, so this
// covers template creation AND edition through the API.
func TestTemplateInputValidatesHomeVolumeMetadata(t *testing.T) {
	base := func() TemplateInput {
		return TemplateInput{Name: "t", DisplayName: "T", OS: "linux", Image: "img:1"}
	}

	good := base()
	good.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{Labels: map[string]string{
		"recurring-job.longhorn.io/source": "enabled",
	}}
	spec, err := specFromInput(good)
	if err != nil {
		t.Fatalf("Longhorn backup label must pass: %v", err)
	}
	if spec.HomeVolume == nil || spec.HomeVolume.Labels["recurring-job.longhorn.io/source"] != "enabled" {
		t.Fatalf("homeVolume must reach the CR spec verbatim, got %+v", spec.HomeVolume)
	}

	badLabel := base()
	badLabel.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{Labels: map[string]string{
		"waas.xorhub.io/retained": "true",
	}}
	if _, err := specFromInput(badLabel); err == nil || !strings.Contains(err.Error(), "homeVolume.labels") {
		t.Fatalf("reserved label must be a 400 naming the field, got %v", err)
	}

	badAnnotation := base()
	badAnnotation.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{Annotations: map[string]string{
		"kubernetes.io/change-cause": "x",
	}}
	if _, err := specFromInput(badAnnotation); err == nil || !strings.Contains(err.Error(), "homeVolume.annotations") {
		t.Fatalf("reserved annotation must be a 400 naming the field, got %v", err)
	}
}
