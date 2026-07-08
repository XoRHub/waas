package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/model"
)

// Events aggregates the Kubernetes Events of one workspace: the CR's own
// (admission decisions, lifecycle milestones the operator emits) plus
// those of every child resource — scheduling failures, image pulls,
// probe flaps. Children are discovered through the operator's managed
// inventory + the workspace label, never a per-feature hardcoded list;
// ReplicaSets are the one addition (created by OUR Deployment, they
// inherit its pod-template labels). Authorization is the workspace's
// (fetchByID: owner or admin) — the UI never talks to the cluster.
func (s *WorkspaceService) Events(ctx context.Context, actor Actor, id string) ([]model.WorkspaceEvent, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	targetNS := ws.EffectiveTargetNamespace()

	// Identity set of the children: (kind, name) in the target namespace.
	type objKey struct{ kind, name string }
	children := map[objKey]bool{}
	gvks := append(waasv1alpha1.WorkspaceContentGVKs(),
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"})
	for _, gvk := range gvks {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		err := s.kube.List(ctx, list, client.InNamespace(targetNS),
			client.MatchingLabels{waasv1alpha1.LabelWorkspace: ws.Name})
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			continue // optional kind (KubeVirt) not installed
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s of workspace %s: %w", gvk.Kind, ws.Name, err)
		}
		for i := range list.Items {
			children[objKey{gvk.Kind, list.Items[i].GetName()}] = true
		}
	}

	// One Events list per involved namespace (CR + target, often equal).
	namespaces := []string{ws.Namespace}
	if targetNS != ws.Namespace {
		namespaces = append(namespaces, targetNS)
	}
	var out []model.WorkspaceEvent
	for _, ns := range namespaces {
		events := &corev1.EventList{}
		if err := s.kube.List(ctx, events, client.InNamespace(ns)); err != nil {
			return nil, fmt.Errorf("listing events in %s: %w", ns, err)
		}
		for i := range events.Items {
			ev := &events.Items[i]
			involved := ev.InvolvedObject
			isCR := ns == ws.Namespace && involved.Kind == "Workspace" && involved.Name == ws.Name
			isChild := ns == targetNS && children[objKey{involved.Kind, involved.Name}]
			if !isCR && !isChild {
				continue
			}
			out = append(out, model.WorkspaceEvent{
				Type:       ev.Type,
				Reason:     ev.Reason,
				Message:    ev.Message,
				ObjectKind: involved.Kind,
				ObjectName: involved.Name,
				Count:      max(ev.Count, 1),
				FirstSeen:  eventFirstSeen(ev),
				LastSeen:   eventLastSeen(ev),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

// Events carry three competing timestamp fields depending on their
// reporting path; pick the most recent meaningful one.
func eventLastSeen(ev *corev1.Event) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}

func eventFirstSeen(ev *corev1.Event) time.Time {
	if !ev.FirstTimestamp.IsZero() {
		return ev.FirstTimestamp.Time
	}
	return eventLastSeen(ev)
}
