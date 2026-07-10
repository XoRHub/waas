package kasmcfg

import (
	"slices"
	"testing"
)

func TestPolicyManagedClipboardKeys(t *testing.T) {
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
		{"invalid yaml", "data_loss_prevention: [unterminated\n", nil, true},
	}
	for _, tc := range cases {
		got, err := PolicyManagedClipboardKeys(tc.raw)
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
