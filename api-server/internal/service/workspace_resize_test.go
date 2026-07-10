package service

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

// fakeExec records the exec calls the resize service makes.
type fakeExec struct {
	calls [][]string // namespace, pod, container, command...
	err   error
}

func (f *fakeExec) Exec(_ context.Context, namespace, pod, container string, command []string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, append([]string{namespace, pod, container}, command...))
	return nil
}

// seedWorkspacePod creates the pod the operator would run for w1, with
// the ownership label the resolver keys on.
func seedWorkspacePod(t *testing.T, f *remoteFixture, phase corev1.PodPhase) {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ws-w1-abc", Namespace: testNS,
			Labels: map[string]string{waasv1alpha1.LabelWorkspace: "w1"},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "desktop", Image: "reg/xfce:1"}}},
		Status: corev1.PodStatus{Phase: phase},
	}
	if err := f.kube.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = phase
	if err := f.kube.Status().Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
}

func TestResizeExecsWaasResizeInThePod(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	runningWorkspace(t, f, "uid-1")
	seedWorkspacePod(t, f, corev1.PodRunning)
	exec := &fakeExec{}
	f.workspace = f.workspace.WithPodExecutor(exec)
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	if err := f.workspace.Resize(ctx, actor, "uid-1", ResizeInput{Width: 2560, Height: 1440}); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("expected exactly one exec, got %v", exec.calls)
	}
	got := exec.calls[0]
	want := []string{testNS, "ws-w1-abc", "desktop", "waas-resize", "2560x1440"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("exec call mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestResizeValidatesBoundsBeforeAnythingElse(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	exec := &fakeExec{}
	f.workspace = f.workspace.WithPodExecutor(exec)
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	for _, in := range []ResizeInput{
		{Width: 0, Height: 1080},
		{Width: 1920, Height: 0},
		{Width: 99, Height: 1080},
		{Width: 1920, Height: 7681},
		{Width: -1920, Height: 1080},
	} {
		if err := f.workspace.Resize(ctx, actor, "uid-1", in); !apierror.IsBadRequest(err) {
			t.Fatalf("%+v must be a bad request, got %v", in, err)
		}
	}
	if len(exec.calls) != 0 {
		t.Fatalf("invalid input must never reach exec, got %v", exec.calls)
	}
}

func TestResizeRejectsRemoteAndNonRunning(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}},
		[]waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	exec := &fakeExec{}
	f.workspace = f.workspace.WithPodExecutor(exec)
	actor := Actor{ID: "u1", Username: "alice", Role: string(auth.RoleUser)}

	// Remote workspaces have no pod: explicit 400, not a bare 404.
	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{
		Name: "lab", Hostname: "10.0.0.5", Port: 22, Protocol: "ssh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.workspace.Resize(ctx, actor, rw.ID, ResizeInput{Width: 1920, Height: 1080}); !apierror.IsBadRequest(err) {
		t.Fatalf("remote workspace resize must be an explicit bad request, got %v", err)
	}

	// Non-Running workspace: conflict, same contract as Reload.
	runningWorkspace(t, f, "uid-1")
	ws, err := f.workspace.findByUID(ctx, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	ws.Status.Phase = waasv1alpha1.PhasePaused
	if err := f.kube.Status().Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	err = f.workspace.Resize(ctx, actor, "uid-1", ResizeInput{Width: 1920, Height: 1080})
	if err == nil || apierror.IsBadRequest(err) || apierror.IsNotFound(err) {
		t.Fatalf("non-running workspace must conflict, got %v", err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("nothing should have been executed, got %v", exec.calls)
	}
}

func TestResizeWithoutPodOrExecutor(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	runningWorkspace(t, f, "uid-1")
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	// No executor wired (dev mode): 503, not a crash.
	if err := f.workspace.Resize(ctx, actor, "uid-1", ResizeInput{Width: 1920, Height: 1080}); err == nil ||
		apierror.IsBadRequest(err) {
		t.Fatalf("resize without executor must be unavailable, got %v", err)
	}

	// Executor wired but no Running pod: conflict from the resolver.
	f.workspace = f.workspace.WithPodExecutor(&fakeExec{})
	seedWorkspacePod(t, f, corev1.PodPending)
	err := f.workspace.Resize(ctx, actor, "uid-1", ResizeInput{Width: 1920, Height: 1080})
	if err == nil || !strings.Contains(err.Error(), "no running pod") {
		t.Fatalf("resize without a running pod must conflict, got %v", err)
	}
}

// Guard against a regression on the ownership rule: another user's
// workspace stays a 404 (never resized, never revealed).
func TestResizeOwnershipIs404(t *testing.T) {
	ctx := context.Background()
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}}, nil)
	runningWorkspace(t, f, "uid-1")
	seedWorkspacePod(t, f, corev1.PodRunning)
	exec := &fakeExec{}
	f.workspace = f.workspace.WithPodExecutor(exec)

	err := f.workspace.Resize(ctx, Actor{ID: "u2", Role: string(auth.RoleUser)}, "uid-1",
		ResizeInput{Width: 1920, Height: 1080})
	if !apierror.IsNotFound(err) {
		t.Fatalf("foreign workspace must stay invisible, got %v", err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("nothing should have been executed, got %v", exec.calls)
	}
}
