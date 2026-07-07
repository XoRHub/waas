package service

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/xorhub/waas/api-server/internal/k8s"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case kind := <-ch:
		return kind
	case <-time.After(2 * time.Second):
		t.Fatal("expected an event, got none")
		return ""
	}
}

func assertSilent(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case kind := <-ch:
		t.Fatalf("expected no event, got %q", kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventHubFansOutByOwner(t *testing.T) {
	hub := NewEventHub()
	alice, cancelAlice := hub.Subscribe("alice", false)
	bob, cancelBob := hub.Subscribe("bob", false)
	admin, cancelAdmin := hub.Subscribe("root", true)
	defer cancelAlice()
	defer cancelBob()
	defer cancelAdmin()

	hub.Notify("workspaces", "alice")
	if got := recv(t, alice); got != "workspaces" {
		t.Fatalf("alice: got %q", got)
	}
	if got := recv(t, admin); got != "workspaces" {
		t.Fatalf("admin must see every owner's events, got %q", got)
	}
	assertSilent(t, bob)

	// Empty owner = broadcast.
	hub.Notify("workspaces", "")
	recv(t, alice)
	recv(t, bob)
	recv(t, admin)

	// Cancel is idempotent and Notify after cancel must not panic.
	cancelBob()
	cancelBob()
	hub.Notify("workspaces", "bob")
}

func TestEventHubRelaysWorkspaceWatch(t *testing.T) {
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatal(err)
	}
	hub := NewEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.RunWorkspaceWatch(ctx, kube, testNS)

	sub, cancelSub := hub.Subscribe("owner-1", false)
	defer cancelSub()
	// Give the watch a beat to be established before mutating.
	time.Sleep(100 * time.Millisecond)

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "w1",
			Namespace: testNS,
			Labels:    map[string]string{ownerLabel: "owner-1"},
		},
		Spec: waasv1alpha1.WorkspaceSpec{TemplateRef: "tpl", Owner: "owner-1"},
	}
	if err := kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, sub); got != "workspaces" {
		t.Fatalf("expected a workspaces event from the watch, got %q", got)
	}
}
