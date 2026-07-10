package service

// Connect-time parameter governance: tuning guacd parameters consumes the
// "protocolParams" right in BOTH the template's overrides.allowedFields
// and the policy's — the same template ∩ policy contract as creation-time
// overrides (policy.CheckOverrides), enforced here because the input
// arrives at session time. The template's per-protocol userParams (exact
// names or cat: category selectors, resolved against the registry) stays
// the fine-grained filter on names.

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

// seedRunningWorkspace seeds the given template plus a Running workspace
// CR stamped from it (status through the subresource, like the operator
// writes it).
func seedRunningWorkspace(t *testing.T, f *remoteFixture, uid string, tpl *waasv1alpha1.WorkspaceTemplate) {
	t.Helper()
	ctx := context.Background()
	if err := f.kube.Create(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "w1", Namespace: testNS, UID: types.UID(uid)},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: tpl.Name, Owner: "u1"},
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

// runningWorkspace is the common case: a template that delegates display
// params wholesale (cat:display → color-depth, encodings, …) plus one
// plain name, and grants protocolParams in its allowedFields.
func runningWorkspace(t *testing.T, f *remoteFixture, uid string) {
	t.Helper()
	seedRunningWorkspace(t, f, uid, &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			OS: waasv1alpha1.OSLinux, Image: "reg/xfce:1",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "vnc", Port: 5901, Default: true,
					UserParams: []string{"cat:display", "enable-audio"}},
			},
			Overrides: &waasv1alpha1.TemplateOverrides{
				AllowedFields: []waasv1alpha1.OverridableField{waasv1alpha1.FieldProtocolParams},
			},
		},
	})
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

	// Params allowed by the TEMPLATE (userParams + allowedFields) but the
	// POLICY does not grant protocolParams: denied.
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

	// Template AND policy grant protocolParams: accepted.
	if _, err := f.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)}, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}}); err != nil {
		t.Fatalf("params with the policy right must pass: %v", err)
	}
}

func TestConnectParamsCategorySelector(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldProtocolParams)})
	runningWorkspace(t, f, "uid-1")
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	// A name delegated only through cat:display: accepted.
	if _, err := f.workspace.Connect(context.Background(), actor, "uid-1",
		ConnectInput{Params: map[string]string{"encodings": "zrle"}}); err != nil {
		t.Fatalf("param delegated via cat: must pass the resolved gate: %v", err)
	}
	// A plain name next to the selector: accepted too (entries are additive).
	if _, err := f.workspace.Connect(context.Background(), actor, "uid-1",
		ConnectInput{Params: map[string]string{"enable-audio": "true", "color-depth": "16"}}); err != nil {
		t.Fatalf("plain name + cat: names must pass: %v", err)
	}
	// A registered name outside the resolved list: still locked.
	_, err := f.workspace.Connect(context.Background(), actor, "uid-1",
		ConnectInput{Params: map[string]string{"read-only": "true"}})
	if err == nil || !strings.Contains(err.Error(), "not user-configurable") {
		t.Fatalf("param outside the resolved list must stay locked, got %v", err)
	}
}

func TestConnectParamsAdminBypass(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "a1", Username: "root", Role: auth.RoleAdmin}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldEnv)})
	runningWorkspace(t, f, "uid-1")
	admin := Actor{ID: "a1", Role: string(auth.RoleAdmin)}
	// Admin bypasses both rights (and ownership: admins see all).
	if _, err := f.workspace.Connect(context.Background(), admin, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}}); err != nil {
		t.Fatalf("admin must bypass the protocolParams rights: %v", err)
	}
}

// The ubuntu-firefox reproduction: userParams delegated but NO overrides
// block at all — the template never allowed connect-time tweaks, so a
// plain user must be rejected even though the name is in userParams and
// no policy stands in the way.
func TestConnectParamsDeniedWithoutTemplateRight(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	seedRunningWorkspace(t, f, "uid-1", &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "firefox", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			OS: waasv1alpha1.OSLinux, Image: "reg/firefox:1",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "vnc", Port: 5901, Default: true,
					UserParams: []string{"enable-audio", "color-depth"}},
			},
		},
	})
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	_, err := f.workspace.Connect(context.Background(), actor, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}})
	if err == nil || !strings.Contains(err.Error(), "does not allow overriding") {
		t.Fatalf("params against a template without the protocolParams right must be denied, got %v", err)
	}

	// No params: plain connect is untouched by the gate.
	if _, err := f.workspace.Connect(context.Background(), actor, "uid-1", ConnectInput{}); err != nil {
		t.Fatalf("plain connect must pass: %v", err)
	}
}

// The template owner bypasses the template gate ("may override any field
// … like an admin") but stays subject to the policy one — the exact
// CheckOverrides contract, mirrored at connect time.
func TestConnectParamsTemplateOwnerBypass(t *testing.T) {
	ownedTemplate := func() *waasv1alpha1.WorkspaceTemplate {
		return &waasv1alpha1.WorkspaceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "owned", Namespace: testNS},
			Spec: waasv1alpha1.WorkspaceTemplateSpec{
				OS: waasv1alpha1.OSLinux, Image: "reg/owned:1",
				Protocols: []waasv1alpha1.WorkspaceProtocol{
					{Name: "vnc", Port: 5901, Default: true,
						UserParams: []string{"color-depth"}},
				},
				// Owner set, but protocolParams NOT in allowedFields.
				Overrides: &waasv1alpha1.TemplateOverrides{Owner: "alice"},
			},
		}
	}

	// Owner + a policy granting protocolParams: accepted despite the
	// template's missing right.
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldProtocolParams)})
	seedRunningWorkspace(t, f, "uid-1", ownedTemplate())
	if _, err := f.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)}, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}}); err != nil {
		t.Fatalf("template owner must bypass the template gate: %v", err)
	}

	// Same workspace ownership but a username that is NOT the template
	// owner: the template gate holds even with a permissive policy.
	f2 := newRemoteFixture(t, []model.User{{ID: "u1", Username: "carol"}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldProtocolParams)})
	seedRunningWorkspace(t, f2, "uid-1", ownedTemplate())
	_, err := f2.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)}, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}})
	if err == nil || !strings.Contains(err.Error(), "does not allow overriding") {
		t.Fatalf("a non-owner must not bypass the template gate, got %v", err)
	}

	// Owner whose policy does NOT grant protocolParams: the policy gate
	// still applies to the owner.
	f3 := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}},
		[]waasv1alpha1.WorkspacePolicy{restrictivePolicy(waasv1alpha1.FieldEnv)})
	seedRunningWorkspace(t, f3, "uid-1", ownedTemplate())
	_, err = f3.workspace.Connect(context.Background(), Actor{ID: "u1", Role: string(auth.RoleUser)}, "uid-1",
		ConnectInput{Params: map[string]string{"color-depth": "16"}})
	if err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("the policy gate must still bind the template owner, got %v", err)
	}
}
