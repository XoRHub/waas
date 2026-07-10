package service

// Retained volumes: home PVCs surviving their deleted workspace. The PVC
// itself is the source of truth (labels stamped by the operator's
// finalizer) — no parallel table to drift. Listings are cluster-wide by
// labels: retained volumes live wherever their workspace was placed.

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// ListRetainedVolumes returns the caller's retained volumes (or every
// user's for admins when all=true).
func (s *WorkspaceService) ListRetainedVolumes(ctx context.Context, actor Actor, all bool) ([]model.RetainedVolume, error) {
	selector := client.MatchingLabels{waasv1alpha1.LabelRetained: "true"}
	if !all {
		selector[waasv1alpha1.LabelOwner] = actor.ID
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := s.kube.List(ctx, pvcs, selector); err != nil {
		return nil, fmt.Errorf("listing retained volumes: %w", err)
	}
	// Owner usernames are resolved for the admin listing only (the fleet
	// view groups by owner); a user's own listing doesn't need them.
	// Best-effort per owner, cached per request — a deleted owner just
	// leaves the field empty, same as WorkspaceService.List.
	usernames := map[string]string{}
	out := make([]model.RetainedVolume, 0, len(pvcs.Items))
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		if !pvc.DeletionTimestamp.IsZero() {
			continue
		}
		v := retainedVolumeToModel(pvc)
		if all {
			name, ok := usernames[v.OwnerID]
			if !ok {
				if u, err := s.users.FindByID(ctx, v.OwnerID); err == nil {
					name = u.Username
				}
				usernames[v.OwnerID] = name
			}
			v.OwnerUsername = name
		}
		out = append(out, v)
	}
	return out, nil
}

// DeleteRetainedVolume deletes one retained volume after ownership
// checks — the caller's own volume, or any volume for admins (asAdmin).
// Both paths are audited: destroying user state must leave a trace.
func (s *WorkspaceService) DeleteRetainedVolume(ctx context.Context, actor Actor, namespace, name string, asAdmin bool) error {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			return apierror.NotFound("volume not found")
		}
		return fmt.Errorf("fetching volume %s/%s: %w", namespace, name, err)
	}
	// Only RETAINED waas volumes are deletable through this API: a live
	// workspace's home goes through the workspace deletion dialog.
	if pvc.Labels[waasv1alpha1.LabelManagedBy] != waasv1alpha1.ManagerName ||
		pvc.Labels[waasv1alpha1.LabelRetained] != "true" {
		return apierror.NotFound("volume not found")
	}
	if !asAdmin && pvc.Labels[waasv1alpha1.LabelOwner] != actor.ID {
		return apierror.NotFound("volume not found")
	}
	if err := s.kube.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting volume %s/%s: %w", namespace, name, err)
	}
	detail := fmt.Sprintf("namespace=%s size=%s owner=%s", namespace,
		pvc.Spec.Resources.Requests.Storage(), pvc.Labels[waasv1alpha1.LabelOwner])
	if asAdmin {
		detail += " via=admin"
	}
	s.audit.Record(ctx, actor, "volume.deleted", "volume", name, detail)
	return nil
}

func retainedVolumeToModel(pvc *corev1.PersistentVolumeClaim) model.RetainedVolume {
	v := model.RetainedVolume{
		Name:            pvc.Name,
		Namespace:       pvc.Namespace,
		Size:            pvc.Spec.Resources.Requests.Storage().String(),
		OwnerID:         pvc.Labels[waasv1alpha1.LabelOwner],
		OriginWorkspace: pvc.Annotations[waasv1alpha1.AnnotationOriginWorkspace],
	}
	if ts, err := time.Parse(time.RFC3339, pvc.Annotations[waasv1alpha1.AnnotationRetainedAt]); err == nil {
		v.RetainedAt = &ts
	}
	return v
}
