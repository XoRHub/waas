package service

// Connect-time parameter governance: tuning guacd parameters consumes the
// policy right "protocolParams" — the same template ∩ policy contract as
// creation-time overrides, enforced here because the input arrives at
// session time. The template's per-protocol userParams stays the
// fine-grained filter on names.

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

// runningWorkspace seeds a Running workspace CR (status through the
// subresource, like the operator writes it).
func runningWorkspace(t *testing.T, f *remoteFixture, uid string) {
	t.Helper()
	ctx := context.Background()
	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			OS: waasv1alpha1.OSLinux, Image: "reg/xfce:1",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "vnc", Port: 5901, Default: true, UserParams: []string{"color-depth"}},
			},
		},
	}
	if err := f.kube.Create(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "w1", Namespace: testNS, UID: types.UID(uid)},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := f.kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	ws.Status = waasv1alpha1.WorkspaceStatus{
		Phase: waasv1alpha1.PhaseRunning, Address: "w1.ns.svc", Port: 5901, Protocol: "vnc",
		Protocols: []waasv1alpha1.WorkspaceProtocolStatus{{Name: "vnc", Port: 5901, Default: true}},
	}
	if err := f.kube.Status().Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
}

func restrictivePolicy(fields ...waasv1alpha1.OverridableField) waasv1alpha1.WorkspacePolicy {
	return waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "restricted"},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			Overrides: &waasv1alpha1.PolicyOverrides{AllowedFields: fields},
		},
	}
}

func TestConnectParamsDeniedWithoutPolicyRight(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldEnv)})
	runningWorkspace(t, f, "uid-1")
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	// Params allowed by the TEMPLATE (userParams) but the POLICY does not
	// grant protocolParams: denied.
	_, err := f.workspace.Connect(context.Background(), actor, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}})
	if err == nil || !strings.Contains(err.Error(), "protocolParams") {
		t.Fatalf("params without the policy right must be denied, got %v", err)
	}

	// No params: plain connect stays allowed under the same policy.
	if _, err := f.workspace.Connect(context.Background(), actor, "uid-1", ConnectInput{}); err != nil {
		t.Fatalf("plain connect must pass: %v", err)
	}
}

func TestConnectParamsAllowedByPolicyOrAdmin(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}, {ID: "a1", Username: "root", Role: auth.RoleAdmin}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldProtocolParams)})
	runningWorkspace(t, f, "uid-1")

	// Policy grants protocolParams: accepted.
	if _, err := f.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)}, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}}); err != nil {
		t.Fatalf("params with the policy right must pass: %v", err)
	}
}

func TestConnectParamsAdminBypass(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "a1", Username: "root", Role: auth.RoleAdmin}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldEnv)})
	runningWorkspace(t, f, "uid-1")
	admin := Actor{ID: "a1", Role: string(auth.RoleAdmin)}
	// Admin bypasses the policy right (and ownership: admins see all).
	if _, err := f.workspace.Connect(context.Background(), admin, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}}); err != nil {
		t.Fatalf("admin must bypass the protocolParams right: %v", err)
	}
}
