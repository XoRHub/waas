package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func tplWith(protocols ...waasv1alpha1.WorkspaceProtocol) *waasv1alpha1.WorkspaceTemplate {
	return &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "ns"},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "T", OS: waasv1alpha1.OSLinux, Image: "img:1",
			Protocols: protocols,
		},
	}
}

func TestTemplateWebhookValidatesParamsAgainstRegistry(t *testing.T) {
	v := &WorkspaceTemplateValidator{}
	ctx := context.Background()

	cases := []struct {
		name    string
		tpl     *waasv1alpha1.WorkspaceTemplate
		wantErr string
	}{
		{
			"valid vnc params",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, Params: map[string]string{"color-depth": "16"}, UserParams: []string{"read-only"}}),
			"",
		},
		{
			"no protocols (legacy synth)",
			tplWith(),
			"",
		},
		{
			"unknown param",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, Params: map[string]string{"bogus": "1"}}),
			"not a registered",
		},
		{
			"platform param in template",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22, Params: map[string]string{"private-key": "-----BEGIN"}}),
			"platform-owned",
		},
		{
			"bad value",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, Params: map[string]string{"color-depth": "12"}}),
			"must be one of",
		},
		{
			"platform param delegated to users",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, UserParams: []string{"password"}}),
			"cannot be delegated",
		},
		{
			"duplicate protocol",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901},
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5902},
			),
			"declared twice",
		},
		{
			"two defaults",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, Default: true},
				waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22, Default: true},
			),
			"at most one",
		},
	}
	for _, tc := range cases {
		_, err := v.ValidateCreate(ctx, tc.tpl)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}
