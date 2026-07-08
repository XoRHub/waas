package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

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

// waitForWatch proves a freshly started RunWatch goroutine is
// established WITHOUT a time.Sleep race: it applies the sentinel
// mutation repeatedly until its event lands on the channel (the watch
// necessarily exists by then), and drains every listed channel so the
// sentinel events never pollute the caller's assertions.
func waitForWatch(t *testing.T, mutate func(i int), drain ...<-chan string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; ; i++ {
		if time.Now().After(deadline) {
			t.Fatal("watch never delivered the sentinel mutation within 5s")
		}
		mutate(i)
		select {
		case <-drain[0]:
			// Established. Drain all channels of buffered sentinels.
			for _, ch := range drain {
				for {
					select {
					case <-ch:
					default:
						goto next
					}
				}
			next:
			}
			return
		case <-time.After(150 * time.Millisecond):
		}
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

	// Sentinel: mutate a dedicated CR until its event proves the watch
	// is established (no sleep race), then drain before asserting.
	sentinel := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "watch-sentinel", Namespace: testNS,
			Labels: map[string]string{ownerLabel: "owner-1"},
		},
		Spec: waasv1alpha1.WorkspaceSpec{TemplateRef: "tpl", Owner: "owner-1"},
	}
	if err := kube.Create(ctx, sentinel); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, func(i int) {
		sentinel.Annotations = map[string]string{"sentinel": fmt.Sprintf("%d", i)}
		_ = kube.Update(ctx, sentinel)
	}, sub)

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

func TestEventHubGenericWatchBroadcastAndOwnerScoping(t *testing.T) {
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatal(err)
	}
	hub := NewEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Templates broadcast (admin-managed, kind-only); volumes are scoped
	// to the PVC's owner label.
	go hub.RunWatch(ctx, kube, &waasv1alpha1.WorkspaceTemplateList{}, "templates", nil)
	go hub.RunWatch(ctx, kube, &corev1.PersistentVolumeClaimList{}, "volumes",
		func(obj k8sclient.Object) string { return obj.GetLabels()[ownerLabel] })

	alice, cancelAlice := hub.Subscribe("alice", false)
	bob, cancelBob := hub.Subscribe("bob", false)
	defer cancelAlice()
	defer cancelBob()

	// Sentinel on the BROADCAST kind (templates) — proving that watch is
	// up also bounds the PVC one (both started together; the PVC
	// assertions below tolerate ordering anyway since alice receives and
	// bob must stay silent regardless).
	sentinel := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-sentinel", Namespace: testNS},
		Spec:       waasv1alpha1.WorkspaceTemplateSpec{DisplayName: "S", OS: waasv1alpha1.OSLinux, Image: "img:s"},
	}
	if err := kube.Create(ctx, sentinel); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, func(i int) {
		sentinel.Annotations = map[string]string{"sentinel": fmt.Sprintf("%d", i)}
		_ = kube.Update(ctx, sentinel)
	}, alice, bob)

	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: testNS},
		Spec:       waasv1alpha1.WorkspaceTemplateSpec{DisplayName: "T", OS: waasv1alpha1.OSLinux, Image: "img:1"},
	}
	if err := kube.Create(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, alice); got != "templates" {
		t.Fatalf("template changes must broadcast, alice got %q", got)
	}
	if got := recv(t, bob); got != "templates" {
		t.Fatalf("template changes must broadcast, bob got %q", got)
	}

	// The PVC watch started with the template one but its establishment
	// is not ordered: prove it with its own sentinel before asserting.
	pvcSentinel := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "watch-sentinel-home", Namespace: testNS,
			Labels: map[string]string{ownerLabel: "alice"},
		},
	}
	if err := kube.Create(ctx, pvcSentinel); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, func(i int) {
		pvcSentinel.Annotations = map[string]string{"sentinel": fmt.Sprintf("%d", i)}
		_ = kube.Update(ctx, pvcSentinel)
	}, alice, bob)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "home-1", Namespace: testNS,
			Labels: map[string]string{ownerLabel: "alice"},
		},
	}
	if err := kube.Create(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, alice); got != "volumes" {
		t.Fatalf("owner must get her volume event, got %q", got)
	}
	assertSilent(t, bob)
}
