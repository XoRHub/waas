package service

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// seedWorkspace creates a Workspace CR straight in the fake cluster (the
// fake client assigns the UID the API exposes as the workspace ID).
func seedWorkspace(t *testing.T, f *remoteFixture, name, owner string) *waasv1alpha1.Workspace {
	t.Helper()
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: owner},
	}
	if err := f.kube.Create(context.Background(), ws); err != nil {
		t.Fatalf("seeding workspace %s: %v", name, err)
	}
	return ws
}

// TestUpdateOverridesReplacesProvidedFields pins the PATCH semantics:
// each provided field replaces the stored override wholesale (empty =
// clear), absent fields stay untouched, and the audit line carries
// field names and env var NAMES only — never values.
func TestUpdateOverridesReplacesProvidedFields(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "marc"}}, nil)
	actor := Actor{ID: "u1", Username: "marc", Role: "user"}
	ws := seedWorkspace(t, f, "marc-box", "u1")
	id := string(ws.UID)

	// No field at all: nothing to do, explicit 400.
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{}); !apierror.IsBadRequest(err) {
		t.Fatalf("empty payload must be a bad request, got %v", err)
	}

	env := []corev1.EnvVar{{Name: "VNC_PW", Value: "super-secret"}, {Name: "HTTP_PROXY", Value: "http://proxy:3128"}}
	sel := map[string]string{"zone": "a"}
	res := map[string]string{"cpu": "2", "memory": "4Gi"}
	got, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{
		Env: &env, NodeSelector: &sel, Resources: &res,
	})
	if err != nil {
		t.Fatalf("update overrides: %v", err)
	}
	if got.Runtime == nil || len(got.Runtime.Env) != 2 || got.Runtime.NodeSelector["zone"] != "a" ||
		got.Runtime.Resources["cpu"] != "2" || got.Runtime.Resources["memory"] != "4Gi" {
		t.Fatalf("the projection must echo the applied runtime, got %+v", got.Runtime)
	}

	fresh := &waasv1alpha1.Workspace{}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides == nil || len(fresh.Spec.Overrides.Env) != 2 || fresh.Spec.Overrides.NodeSelector["zone"] != "a" {
		t.Fatalf("CR overrides not applied: %+v", fresh.Spec.Overrides)
	}
	if fresh.Spec.Resources == nil || fresh.Spec.Resources.Requests.Cpu().String() != "2" {
		t.Fatalf("CR resources not applied: %+v", fresh.Spec.Resources)
	}
	if !fresh.Spec.Resources.Limits.Memory().Equal(*fresh.Spec.Resources.Requests.Memory()) {
		t.Fatal("resources must keep the requests == limits contract of creation")
	}

	// Replacement, not merge: an empty env clears it; nodeSelector and
	// resources are ABSENT from this call and must survive untouched.
	empty := []corev1.EnvVar{}
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{Env: &empty}); err != nil {
		t.Fatalf("clearing env: %v", err)
	}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides == nil || len(fresh.Spec.Overrides.Env) != 0 || fresh.Spec.Overrides.NodeSelector["zone"] != "a" {
		t.Fatalf("env must clear, nodeSelector must survive: %+v", fresh.Spec.Overrides)
	}
	if fresh.Spec.Resources == nil {
		t.Fatal("resources must survive an env-only update")
	}

	// Clearing the LAST override drops the block entirely (nil, as a
	// creation without overrides), and an empty resources map reverts to
	// the template sizing (presence = override).
	noSel := map[string]string{}
	noRes := map[string]string{}
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{NodeSelector: &noSel, Resources: &noRes}); err != nil {
		t.Fatalf("clearing the rest: %v", err)
	}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides != nil {
		t.Fatalf("an all-empty overrides block must store nil, got %+v", fresh.Spec.Overrides)
	}
	if fresh.Spec.Resources != nil {
		t.Fatalf("an empty resources map must revert to the template sizing, got %+v", fresh.Spec.Resources)
	}

	// Audit safety: names yes, values never.
	logs, _, err := f.auditSvc.List(ctx, repository.AuditFilter{Action: "workspace.overrides_updated"}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("expected workspace.overrides_updated audit entries")
	}
	joined := ""
	for _, l := range logs {
		joined += l.Detail + "\n"
	}
	if !strings.Contains(joined, "env=VNC_PW,HTTP_PROXY") {
		t.Fatalf("audit must name the env vars, got %q", joined)
	}
	if strings.Contains(joined, "super-secret") || strings.Contains(joined, "proxy:3128") {
		t.Fatalf("audit leaks env values: %q", joined)
	}
}

// TestUpdateOverridesScoping: someone else's workspace is invisible
// (404, not 403 — existence must not leak).
func TestUpdateOverridesScoping(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "marc"}, {ID: "u2", Username: "eve"}}, nil)
	ws := seedWorkspace(t, f, "marc-box", "u1")
	env := []corev1.EnvVar{{Name: "X", Value: "y"}}
	if _, err := f.workspace.UpdateOverrides(ctx, Actor{ID: "u2", Username: "eve", Role: "user"},
		string(ws.UID), UpdateOverridesInput{Env: &env}); !apierror.IsNotFound(err) {
		t.Fatalf("other owner's workspace must be a 404, got %v", err)
	}
}

// TestReloadStampsTheOneShotAnnotation pins the reload contract: only a
// Running workspace reloads, the dedicated annotation is stamped, and
// neither spec.paused nor the manual-state-at annotation move (schedule
// conflict rule B must stay undisturbed).
func TestReloadStampsTheOneShotAnnotation(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "marc"}}, nil)
	actor := Actor{ID: "u1", Username: "marc", Role: "user"}
	ws := seedWorkspace(t, f, "marc-box", "u1")
	id := string(ws.UID)

	// Not running yet: nothing to reload.
	if _, err := f.workspace.Reload(ctx, actor, id); err == nil || !strings.Contains(err.Error(), "not Running") {
		t.Fatalf("reload of a non-running workspace must conflict, got %v", err)
	}

	ws.Status.Phase = waasv1alpha1.PhaseRunning
	if err := f.kube.Status().Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Reload(ctx, actor, id); err != nil {
		t.Fatalf("reload: %v", err)
	}

	fresh := &waasv1alpha1.Workspace{}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Annotations[waasv1alpha1.AnnotationReloadRequestedAt] == "" {
		t.Fatal("reload must stamp the one-shot annotation")
	}
	if fresh.Spec.Paused {
		t.Fatal("reload must never touch spec.paused")
	}
	if _, ok := fresh.Annotations[waasv1alpha1.AnnotationManualStateAt]; ok {
		t.Fatal("reload must never stamp manual-state-at (schedule rule B)")
	}

	logs, _, err := f.auditSvc.List(ctx, repository.AuditFilter{Action: "workspace.reloaded"}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one workspace.reloaded audit entry, got %d", len(logs))
	}
}
