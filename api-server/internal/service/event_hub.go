package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// EventHub fans platform change notifications out to the connected SSE
// streams. Events carry only a KIND ("workspaces", "remote-workspaces") —
// never object payloads: subscribers re-fetch through the normal
// authorized API, so the hub can never leak someone else's data, and the
// frontend keeps its single source of truth (the queries).
//
// Workspace changes come from ONE shared Kubernetes watch (whatever
// mutated the CR: portal, kubectl, the operator's status updates, cron
// transitions). Remote-workspace changes are DB-backed and only flow
// through this api-server, so the mutations notify directly.
type EventHub struct {
	mu   sync.Mutex
	subs map[*eventSub]struct{}
}

type eventSub struct {
	ch      chan string
	ownerID string
	admin   bool
}

func NewEventHub() *EventHub {
	return &EventHub{subs: map[*eventSub]struct{}{}}
}

// Subscribe registers a stream for one authenticated user. The returned
// cancel MUST be called when the stream ends.
func (h *EventHub) Subscribe(ownerID string, admin bool) (<-chan string, func()) {
	sub := &eventSub{ch: make(chan string, 16), ownerID: ownerID, admin: admin}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, live := h.subs[sub]; live {
			delete(h.subs, sub)
			close(sub.ch)
		}
	}
	return sub.ch, cancel
}

// Notify wakes the subscribers concerned by a change: the owner's
// streams and every admin stream (empty ownerID = everyone). Sends never
// block — a slow consumer just coalesces into its pending notification.
func (h *EventHub) Notify(kind, ownerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.admin || ownerID == "" || sub.ownerID == ownerID {
			select {
			case sub.ch <- kind:
			default:
			}
		}
	}
}

// RunWorkspaceWatch relays cluster Workspace changes into the hub until
// ctx ends, restarting the watch with a small backoff on failure.
func (h *EventHub) RunWorkspaceWatch(ctx context.Context, kube client.WithWatch, namespace string) {
	h.RunWatch(ctx, kube, &waasv1alpha1.WorkspaceList{}, "workspaces",
		func(obj client.Object) string { return obj.GetLabels()[ownerLabel] },
		client.InNamespace(namespace))
}

// RunWatch relays cluster changes of one list type into the hub as the
// given kind until ctx ends, restarting the watch with a small backoff
// on failure. ownerOf scopes delivery to that owner's streams (admins
// see everything); nil broadcasts — right for admin-managed objects
// (templates, catalog, policies) whose events carry no data anyway: the
// re-fetch goes through the normal per-user authorized API.
func (h *EventHub) RunWatch(ctx context.Context, kube client.WithWatch, list client.ObjectList,
	kind string, ownerOf func(client.Object) string, opts ...client.ListOption) {
	for ctx.Err() == nil {
		w, err := kube.Watch(ctx, list, opts...)
		if err != nil {
			slog.Warn("watch failed; retrying", "kind", kind, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for ev := range w.ResultChan() {
			obj, ok := ev.Object.(client.Object)
			if !ok {
				continue
			}
			owner := ""
			if ownerOf != nil {
				owner = ownerOf(obj)
			}
			h.Notify(kind, owner)
		}
		w.Stop()
	}
}
