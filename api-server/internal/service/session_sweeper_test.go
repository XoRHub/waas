package service

// Session hygiene at deletion: no session row may stay "open" once its
// target is gone — whichever path deleted the target (API with its
// immediate close, kubectl/ArgoCD covered by the sweeper).

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/shared/auth"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func openSession(t *testing.T, f *remoteFixture, id, workspaceID, kind string) {
	t.Helper()
	err := f.sessions().Create(context.Background(), &model.Session{
		ID: id, UserID: "u1", WorkspaceID: workspaceID, WorkspaceName: "ws-" + id,
		Protocol: "vnc", StartedAt: time.Now().UTC().Add(-time.Hour), Kind: kind,
	})
	if err != nil {
		t.Fatalf("seeding session %s: %v", id, err)
	}
}

func assertEnded(t *testing.T, f *remoteFixture, id string, wantEnded bool) {
	t.Helper()
	s, err := f.sessions().FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("finding session %s: %v", id, err)
	}
	if (s.EndedAt != nil) != wantEnded {
		t.Fatalf("session %s: ended=%v, want ended=%v", id, s.EndedAt != nil, wantEnded)
	}
}

func TestSessionSweeperEndsOrphanedSessions(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	ctx := context.Background()

	// A live workspace CR and a live remote row.
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "alive", Namespace: testNS, UID: types.UID("uid-alive")},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := f.kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	rw := &model.RemoteWorkspace{ID: "rw-alive", OwnerID: "u1", Name: "nas", Hostname: "nas.local",
		Protocols: []model.RemoteProtocol{{Name: "ssh", Port: 22, Default: true}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.remotes.Create(ctx, rw); err != nil {
		t.Fatal(err)
	}

	openSession(t, f, "s-live", "uid-alive", model.SessionKindWorkspace)
	openSession(t, f, "s-orphan", "uid-deleted", model.SessionKindWorkspace)
	openSession(t, f, "s-remote-live", "rw-alive", model.SessionKindRemote)
	openSession(t, f, "s-remote-orphan", "rw-deleted", model.SessionKindRemote)

	sweeper := NewSessionSweeper(f.kube, testNS, f.sessions(), f.remotes, f.audit(), time.Minute)
	if err := sweeper.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	assertEnded(t, f, "s-live", false)
	assertEnded(t, f, "s-orphan", true)
	assertEnded(t, f, "s-remote-live", false)
	assertEnded(t, f, "s-remote-orphan", true)

	// Idempotence: a second pass must not fail nor re-touch anything.
	if err := sweeper.sweep(ctx); err != nil {
		t.Fatalf("second sweep must be a no-op: %v", err)
	}
}

func TestWorkspaceDeleteEndsItsOpenSessions(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	ctx := context.Background()

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "doomed", Namespace: testNS, UID: types.UID("uid-doomed")},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := f.kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	openSession(t, f, "s-open", "uid-doomed", model.SessionKindWorkspace)

	admin := Actor{ID: "u1", Role: string(auth.RoleAdmin)}
	if err := f.workspace.Delete(ctx, admin, "uid-doomed", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertEnded(t, f, "s-open", true)
}

func TestRemoteWorkspaceDeleteEndsItsOpenSessions(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, []waasv1alpha1.WorkspacePolicy{remotePolicy(true)})
	ctx := context.Background()
	actor := Actor{ID: "u1", Role: string(auth.RoleUser)}

	rw, err := f.remote.Create(ctx, actor, RemoteWorkspaceInput{Name: "nas", Hostname: "nas.local", Protocol: "ssh", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	openSession(t, f, "s-remote", rw.ID, model.SessionKindRemote)

	if err := f.remote.Delete(ctx, actor, rw.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertEnded(t, f, "s-remote", true)
}
