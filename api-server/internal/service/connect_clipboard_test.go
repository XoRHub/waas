package service

// Clipboard governance at connect time: the policy grant
// (WorkspacePolicy.spec.clipboard) is clamped by the session's effective
// disable-copy/disable-paste params — template-locked values overlaid
// with the vetted connect-time overrides, the same precedence guacd
// sees. Params can only restrict the policy, never override a denial.
// The clamped grant feeds BOTH the signed connection token (what wwt
// enforces) and ConnectResult.Capabilities (what the session menu
// shows), so every case asserts the two stay identical.

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

// clipboardPolicy grants/denies the two clipboard directions and opens
// protocolParams overrides (so connect-time disable-* tweaks pass the
// rights gate). RemoteWorkspaces is on for the remote-connect tests.
func clipboardPolicy(copyFrom, pasteTo bool) waasv1alpha1.WorkspacePolicy {
	return waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "clipboard"},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			RemoteWorkspaces: true,
			Clipboard:        &waasv1alpha1.ClipboardPolicy{CopyFromWorkspace: &copyFrom, PasteToWorkspace: &pasteTo},
			Overrides: &waasv1alpha1.PolicyOverrides{
				AllowedFields: []waasv1alpha1.OverridableField{waasv1alpha1.FieldProtocolParams},
			},
		},
	}
}

// clipboardTemplate locks the given params on its vnc entry and
// delegates the two clipboard names to connect-time overrides.
func clipboardTemplate(locked map[string]string) *waasv1alpha1.WorkspaceTemplate {
	return &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			OS: waasv1alpha1.OSLinux, Image: "reg/xfce:1",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "vnc", Port: 5901, Default: true,
					Params:     locked,
					UserParams: []string{"disable-copy", "disable-paste"}},
			},
			Overrides: &waasv1alpha1.TemplateOverrides{
				AllowedFields: []waasv1alpha1.OverridableField{waasv1alpha1.FieldProtocolParams},
			},
		},
	}
}

// assertClipboard checks the capabilities AND the signed token carry the
// same clamped grant — a divergence between the two is the original bug
// (menu says allowed, wwt lets it through / or the reverse).
func assertClipboard(t *testing.T, f *remoteFixture, res *ConnectResult, wantCopy, wantPaste bool) {
	t.Helper()
	if res.Capabilities.ClipboardCopy != wantCopy || res.Capabilities.ClipboardPaste != wantPaste {
		t.Fatalf("capabilities copy=%t paste=%t, want copy=%t paste=%t",
			res.Capabilities.ClipboardCopy, res.Capabilities.ClipboardPaste, wantCopy, wantPaste)
	}
	claims, err := auth.VerifyConnectionToken(res.ConnectionToken, "waas-test", f.signer.Public())
	if err != nil {
		t.Fatalf("verifying connection token: %v", err)
	}
	if claims.Clipboard.Copy != wantCopy || claims.Clipboard.Paste != wantPaste {
		t.Fatalf("token clipboard copy=%t paste=%t diverges from capabilities copy=%t paste=%t",
			claims.Clipboard.Copy, claims.Clipboard.Paste, wantCopy, wantPaste)
	}
}

func TestConnectClipboardClampedByParams(t *testing.T) {
	cases := []struct {
		name                    string
		policyCopy, policyPaste bool
		locked                  map[string]string
		overrides               map[string]string
		wantCopy, wantPaste     bool
	}{
		{name: "policy alone decides when params are absent",
			policyCopy: true, policyPaste: true,
			wantCopy: true, wantPaste: true},
		{name: "policy denial holds when params are absent",
			policyCopy: false, policyPaste: false,
			wantCopy: false, wantPaste: false},
		{name: "template-locked disable-copy blocks on a plain connect",
			policyCopy: true, policyPaste: true,
			locked:   map[string]string{"disable-copy": "true"},
			wantCopy: false, wantPaste: true},
		{name: "connect-time disable-paste blocks despite the policy grant",
			policyCopy: true, policyPaste: true,
			overrides: map[string]string{"disable-paste": "true"},
			wantCopy:  true, wantPaste: false},
		{name: "false params never override a policy denial",
			policyCopy: false, policyPaste: false,
			locked:    map[string]string{"disable-copy": "false"},
			overrides: map[string]string{"disable-paste": "false"},
			wantCopy:  false, wantPaste: false},
		{name: "param true and policy denial both block",
			policyCopy: false, policyPaste: true,
			locked:   map[string]string{"disable-copy": "true"},
			wantCopy: false, wantPaste: true},
		{name: "malformed template value fails closed",
			policyCopy: true, policyPaste: true,
			locked:   map[string]string{"disable-copy": "definitely"},
			wantCopy: false, wantPaste: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}},
				[]waasv1alpha1.WorkspacePolicy{clipboardPolicy(tc.policyCopy, tc.policyPaste)})
			seedRunningWorkspace(t, f, "uid-1", clipboardTemplate(tc.locked))
			res, err := f.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)},
				"uid-1", ConnectInput{Params: tc.overrides})
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			assertClipboard(t, f, res, tc.wantCopy, tc.wantPaste)
		})
	}
}

func TestRemoteConnectClipboardClampedByParams(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "u1"}},
		[]waasv1alpha1.WorkspacePolicy{clipboardPolicy(true, true)})
	actor := Actor{ID: "u1", Username: "u1", Role: string(auth.RoleUser)}
	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "lab", Hostname: "10.0.0.5", Port: 3389, Protocol: "rdp",
		Params: map[string]string{"disable-copy": "true"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The stored registration param alone clamps copy — no tweak given.
	res, err := f.remote.Connect(ctx, actor, rw.ID, ConnectInput{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	assertClipboard(t, f, res, false, true)

	// A connect-time tweak clamps paste on top of the stored param.
	res, err = f.remote.Connect(ctx, actor, rw.ID,
		ConnectInput{Params: map[string]string{"disable-paste": "true"}})
	if err != nil {
		t.Fatalf("connect with tweak: %v", err)
	}
	assertClipboard(t, f, res, false, false)
}
