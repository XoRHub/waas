package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	env := []corev1.EnvVar{{Name: "WAAS_DESKTOP_PASSWORD", Value: "super-secret"}, {Name: "HTTP_PROXY", Value: "http://proxy:3128"}}
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
	if !strings.Contains(joined, "env=WAAS_DESKTOP_PASSWORD,HTTP_PROXY") {
		t.Fatalf("audit must name the env vars, got %q", joined)
	}
	if strings.Contains(joined, "super-secret") || strings.Contains(joined, "proxy:3128") {
		t.Fatalf("audit leaks env values: %q", joined)
	}
}

// TestUpdateOverridesMetadataAndSchedule pins the same PATCH semantics
// for the metadata (labels/annotations) and schedule overrides: a
// provided field replaces the stored override, absent fields survive,
// and an empty map / zero schedule struct clears — "cleared" and "never
// set" are the same CR state.
func TestUpdateOverridesMetadataAndSchedule(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "marc"}}, nil)
	actor := Actor{ID: "u1", Username: "marc", Role: "user"}
	ws := seedWorkspace(t, f, "marc-box", "u1")
	id := string(ws.UID)

	env := []corev1.EnvVar{{Name: "X", Value: "y"}}
	sched := &waasv1alpha1.WorkspaceSchedule{Timezone: "Europe/Paris", Uptime: []string{"0 8 * * 1-5"}}
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{Env: &env, Schedule: sched}); err != nil {
		t.Fatalf("seeding env+schedule overrides: %v", err)
	}

	// Labels alone: env and schedule must survive untouched.
	labels := map[string]string{"team": "blue"}
	got, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{Labels: &labels})
	if err != nil {
		t.Fatalf("patching labels: %v", err)
	}
	if got.Runtime == nil || got.Runtime.Labels["team"] != "blue" ||
		len(got.Runtime.Env) != 1 || got.Runtime.Schedule == nil {
		t.Fatalf("labels must land, env and schedule must survive: %+v", got.Runtime)
	}

	fresh := &waasv1alpha1.Workspace{}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides.Labels["team"] != "blue" || fresh.Spec.Overrides.Schedule == nil {
		t.Fatalf("CR must carry labels AND the untouched schedule: %+v", fresh.Spec.Overrides)
	}

	// Annotations set + labels cleared with an empty map in one call.
	ann := map[string]string{"waas.example.com/note": "hi"}
	noLabels := map[string]string{}
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{Annotations: &ann, Labels: &noLabels}); err != nil {
		t.Fatalf("annotations + label clear: %v", err)
	}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides.Labels != nil || fresh.Spec.Overrides.Annotations["waas.example.com/note"] != "hi" {
		t.Fatalf("labels must clear to nil, annotations must land: %+v", fresh.Spec.Overrides)
	}

	// A ZERO schedule struct clears the override (absent would leave it).
	if _, err := f.workspace.UpdateOverrides(ctx, actor, id, UpdateOverridesInput{Schedule: &waasv1alpha1.WorkspaceSchedule{}}); err != nil {
		t.Fatalf("clearing schedule: %v", err)
	}
	if err := f.kube.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "marc-box"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Overrides == nil || fresh.Spec.Overrides.Schedule != nil {
		t.Fatalf("zero schedule must clear the override only: %+v", fresh.Spec.Overrides)
	}
	if len(fresh.Spec.Overrides.Env) != 1 || fresh.Spec.Overrides.Annotations == nil {
		t.Fatalf("env and annotations must survive the schedule clear: %+v", fresh.Spec.Overrides)
	}
}

// denyingUpdate simulates the governance webhook: every Update comes
// back Forbidden with the webhook's "[Reason] message" denial format.
type denyingUpdate struct {
	client.Client
	denial string
}

func (d *denyingUpdate) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return apierrors.NewForbidden(
		schema.GroupResource{Group: "waas.xorhub.io", Resource: "workspaces"}, "marc-box",
		errors.New(d.denial))
}

// TestUpdateOverridesWebhookDenial: the service never judges override
// rights itself — a webhook denial surfaces as a 403 carrying the
// "[Reason] message" tail, and the denial is audited.
func TestUpdateOverridesWebhookDenial(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "marc"}}, nil)
	actor := Actor{ID: "u1", Username: "marc", Role: "user"}
	ws := seedWorkspace(t, f, "marc-box", "u1")

	denial := `admission webhook "vworkspace.kb.io" denied the request: [OverrideNotAllowed] template does not allow overriding "metadata"`
	svc := NewWorkspaceService(&denyingUpdate{Client: f.kube, denial: denial}, testNS,
		f.users, f.sessionsDB, f.auditSvc, f.signer, "waas-test", time.Minute)

	labels := map[string]string{"team": "blue"}
	_, err := svc.UpdateOverrides(ctx, actor, string(ws.UID), UpdateOverridesInput{Labels: &labels})
	if !apierror.IsForbidden(err) {
		t.Fatalf("webhook denial must surface as a 403, got %v", err)
	}
	if !strings.Contains(err.Error(), `[OverrideNotAllowed] template does not allow overriding "metadata"`) {
		t.Fatalf("the denial must keep the webhook's [Reason] message, got %q", err.Error())
	}

	logs, _, err := f.auditSvc.List(ctx, repository.AuditFilter{Action: "workspace.denied"}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one workspace.denied audit entry, got %d", len(logs))
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
