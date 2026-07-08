package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// User-level KasmVNC configuration (WorkspaceTemplate.spec.kasmvncConfig).
//
// The opaque string is materialized as a per-workspace ConfigMap in the
// pod's namespace (mounted with subPath at <home>/.vnc/kasmvnc.yaml so
// the .vnc directory stays writable for KasmVNC's runtime artifacts —
// self.pem lives there). The ConfigMap shares the workload's name: the
// teardown finalizer's name-based sweep and the janitor's content check
// cover it with zero extra code. subPath mounts never refresh in place,
// so the content hash is stamped on the pod template
// (annotationKasmConfigHash) — a template edit rolls the workload.

const (
	kasmConfigKey            = "kasmvnc.yaml"
	kasmConfigVolume         = "kasmvnc-config"
	annotationKasmConfigHash = "waas.xorhub.io/kasmvnc-config-hash"
)

func kasmConfigHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

// ensureKasmConfig keeps the per-workspace ConfigMap converged on the
// template's content, and removes it when the field goes empty.
func (r *WorkspaceReconciler) ensureKasmConfig(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	key := computeKey(ws)
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, key, existing)

	if tpl.Spec.KasmVNCConfig == "" {
		// Field cleared: drop the mount's source; the hash annotation
		// change rolls the pod off the stale file.
		if err == nil {
			if derr := r.Delete(ctx, existing); derr != nil && !apierrors.IsNotFound(derr) {
				return fmt.Errorf("deleting kasmvnc config %s: %w", key.Name, derr)
			}
		}
		return nil
	}

	switch {
	case apierrors.IsNotFound(err):
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Labels:    workspaceLabels(ws),
			},
			Data: map[string]string{kasmConfigKey: tpl.Spec.KasmVNCConfig},
		}
		if err := r.setOwnerIfLocal(ws, cm); err != nil {
			return fmt.Errorf("setting owner on kasmvnc config: %w", err)
		}
		if err := r.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating kasmvnc config %s: %w", key.Name, err)
		}
	case err != nil:
		return fmt.Errorf("fetching kasmvnc config %s: %w", key.Name, err)
	case existing.Data[kasmConfigKey] != tpl.Spec.KasmVNCConfig:
		existing.Data = map[string]string{kasmConfigKey: tpl.Spec.KasmVNCConfig}
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("syncing kasmvnc config %s: %w", key.Name, err)
		}
	}
	return nil
}

// kasmConfigDrifted compares a live pod template's config hash with the
// template's desired one ("" both when the feature is unused, so
// non-kasm workloads never drift).
func kasmConfigDrifted(podAnnotations map[string]string, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	want := ""
	if tpl.Spec.KasmVNCConfig != "" {
		want = kasmConfigHash(tpl.Spec.KasmVNCConfig)
	}
	return podAnnotations[annotationKasmConfigHash] != want
}
