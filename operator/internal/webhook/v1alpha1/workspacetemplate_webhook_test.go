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
			// cat: selectors are valid userParams entries, not unknown
			// param names — including next to a name of the same category
			// (additive, redundant but never contradictory).
			"category selector in userParams",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, UserParams: []string{"cat:audio", "audio-servername", "color-depth"}}),
			"",
		},
		{
			"unknown category selector",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, UserParams: []string{"cat:bogus"}}),
			"unknown category",
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
		{
			"clean kasmvnc",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901, Default: true}),
			"",
		},
		{
			// The guacd protocols combine freely between themselves.
			"vnc + rdp + ssh without kasmvnc",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, Default: true},
				waasv1alpha1.WorkspaceProtocol{Name: "rdp", Port: 3389},
				waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22},
			),
			"",
		},
		{
			// kasmvnc is exclusive: it bypasses guacd and its generated
			// password would collide with the desktop one (VNC_PW, shared
			// pod-copy Secret name).
			"kasmvnc combined with vnc",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901},
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901},
			),
			"kasmvnc cannot be combined",
		},
		{
			"kasmvnc combined with rdp",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901},
				waasv1alpha1.WorkspaceProtocol{Name: "rdp", Port: 3389},
			),
			"kasmvnc cannot be combined",
		},
		{
			"kasmvnc combined with ssh",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901},
				waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22},
			),
			"kasmvnc cannot be combined",
		},
		{
			"kasmvnc combined with all three guacd protocols",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901},
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901},
				waasv1alpha1.WorkspaceProtocol{Name: "rdp", Port: 3389},
				waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22},
			),
			"kasmvnc cannot be combined",
		},
		{
			// kasmvnc bypasses guacd: no guacd param may be attached.
			"kasmvnc with guacd params",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901, Params: map[string]string{"color-depth": "16"}}),
			"not a registered",
		},
		{
			"kasmvnc with delegated params",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901, UserParams: []string{"read-only"}}),
			"not a registered",
		},
		{
			"kasmvncConfig without kasmvnc protocol",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901})
				tpl.Spec.KasmVNCConfig = "desktop: {}"
				return tpl
			}(),
			"requires a kasmvnc protocol",
		},
		{
			"kasmvncConfig with kasmvnc protocol",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.KasmVNCConfig = "desktop: {}"
				return tpl
			}(),
			"",
		},
		{
			// The operator owns the clipboard enable flags — an admin who
			// sets them here would be silently overridden, so refuse.
			"kasmvncConfig setting a policy-managed clipboard key",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.KasmVNCConfig = "data_loss_prevention:\n  clipboard:\n    server_to_client:\n      enabled: true\n"
				return tpl
			}(),
			"derived from WorkspacePolicy.Clipboard",
		},
		{
			// Non-managed clipboard sub-keys stay the admin's to tune.
			"kasmvncConfig with only unmanaged clipboard sub-keys",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.KasmVNCConfig = "data_loss_prevention:\n  clipboard:\n    server_to_client:\n      size: unlimited\n"
				return tpl
			}(),
			"",
		},
		{
			"kasmvncConfig with the client-override flag",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.KasmVNCConfig = "runtime_configuration:\n  allow_client_to_override_kasm_server_settings: true\n"
				return tpl
			}(),
			"derived from WorkspacePolicy.Clipboard",
		},
		{
			"kasmvncConfig with invalid YAML",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.KasmVNCConfig = "data_loss_prevention: [unterminated\n"
				return tpl
			}(),
			"not valid YAML",
		},
		{
			"kasmvnc on windows",
			func() *waasv1alpha1.WorkspaceTemplate {
				tpl := tplWith(waasv1alpha1.WorkspaceProtocol{Name: "kasmvnc", Port: 6901})
				tpl.Spec.OS = waasv1alpha1.OSWindows
				return tpl
			}(),
			"not available on windows",
		},
		{
			"audio port on vnc",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, ExposeAudioPort: true, Params: map[string]string{"enable-audio": "true"}}),
			"",
		},
		{
			// The PulseAudio port only serves guacd's VNC audio path.
			"audio port on ssh",
			tplWith(waasv1alpha1.WorkspaceProtocol{Name: "ssh", Port: 22, ExposeAudioPort: true}),
			"only the vnc protocol",
		},
		{
			// A protocol squatting 4713 would duplicate the pod/Service port.
			"audio port colliding with a protocol port",
			tplWith(
				waasv1alpha1.WorkspaceProtocol{Name: "vnc", Port: 5901, ExposeAudioPort: true},
				waasv1alpha1.WorkspaceProtocol{Name: "rdp", Port: 4713},
			),
			"collides with the exposed PulseAudio port",
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

func TestTemplateWebhookValidatesPlacement(t *testing.T) {
	v := &WorkspaceTemplateValidator{}
	ctx := context.Background()

	good := tplWith()
	good.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-{user}"}
	if _, err := v.ValidateCreate(ctx, good); err != nil {
		t.Fatalf("valid placement pattern must pass: %v", err)
	}

	badPattern := tplWith()
	badPattern.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "WAAS-{user}"}
	if _, err := v.ValidateCreate(ctx, badPattern); err == nil || !strings.Contains(err.Error(), "placement.namespace") {
		t.Fatalf("invalid literal in pattern must be denied, got %v", err)
	}

	badLabel := tplWith()
	badLabel.Spec.Placement = &waasv1alpha1.WorkspacePlacement{
		Namespace:       "waas-{user}",
		NamespaceLabels: map[string]string{"pod-security.kubernetes.io/enforce": "privileged"},
	}
	if _, err := v.ValidateCreate(ctx, badLabel); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("PSA escalation through namespaceLabels must be denied, got %v", err)
	}

	badWorkload := tplWith()
	badWorkload.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{
		Annotations: map[string]string{"sidecar.istio.io/inject": "true"},
	}
	if _, err := v.ValidateCreate(ctx, badWorkload); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("injector annotations must be denied, got %v", err)
	}
}

func TestTemplateWebhookValidatesHomeVolumeMetadata(t *testing.T) {
	v := &WorkspaceTemplateValidator{}
	ctx := context.Background()

	// The driving use case: Longhorn recurring-job enrollment by labels.
	good := tplWith()
	good.Spec.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{
		Labels: map[string]string{
			"recurring-job.longhorn.io/source":             "enabled",
			"recurring-job-group.longhorn.io/backup-daily": "enabled",
		},
	}
	if _, err := v.ValidateCreate(ctx, good); err != nil {
		t.Fatalf("Longhorn backup labels must pass: %v", err)
	}

	badLabel := tplWith()
	badLabel.Spec.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{
		Labels: map[string]string{"waas.xorhub.io/retained": "true"},
	}
	if _, err := v.ValidateCreate(ctx, badLabel); err == nil || !strings.Contains(err.Error(), "homeVolume.labels") {
		t.Fatalf("platform label through homeVolume.labels must be denied, got %v", err)
	}

	badAnnotation := tplWith()
	badAnnotation.Spec.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{
		Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}"},
	}
	if _, err := v.ValidateCreate(ctx, badAnnotation); err == nil || !strings.Contains(err.Error(), "homeVolume.annotations") {
		t.Fatalf("reserved annotation through homeVolume.annotations must be denied, got %v", err)
	}
}
