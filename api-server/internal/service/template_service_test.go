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
