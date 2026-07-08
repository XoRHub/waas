package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// Generated KasmVNC credentials.
//
// kasmweb/* images authenticate their web endpoint with HTTP Basic
// (fixed user kasm_user, password from the VNC_PW env). When a template
// serves the kasmvnc protocol WITHOUT an explicit password source
// (template/override env, credentialsSecretRef), the operator generates
// a random per-workspace password so that no two workspaces share one
// and nothing secret lands in a CR. It materializes as TWO Secrets:
//
//   - waas-kasm-<workspace> in the CR namespace: the copy the api-server
//     resolves at connect time (its Secret RBAC is scoped there).
//     Owner-referenced to the Workspace, so it is garbage-collected with
//     the CR.
//   - <workloadName> in the pod namespace: what the pod's VNC_PW
//     secretKeyRef reads (env can only reference Secrets in the pod's
//     own namespace — the dev-ssh constraint in docs/placement.md).
//     Named like every other content object so the teardown finalizer's
//     name-based sweep deletes it, and labeled so the namespace janitor
//     counts it as content.

// kasmSecretName is the resolver-side copy, next to the Workspace CR.
func kasmSecretName(ws *waasv1alpha1.Workspace) string { return "waas-kasm-" + ws.Name }

// kasmPasswordGenerated says whether THIS workspace runs with an
// operator-generated KasmVNC password. Must stay aligned with the
// api-server's fallback (password still empty after every explicit
// source → read the generated Secret).
func kasmPasswordGenerated(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		return false
	}
	var kasm *waasv1alpha1.WorkspaceProtocol
	for _, p := range tpl.Spec.EffectiveProtocols() {
		if p.Name == string(waasv1alpha1.ProtocolKasmVNC) {
			kasm = &p
			break
		}
	}
	if kasm == nil || kasm.CredentialsSecretRef != "" {
		return false
	}
	var overrides []corev1.EnvVar
	if ws.Spec.Overrides != nil {
		overrides = ws.Spec.Overrides.Env
	}
	for _, env := range mergeEnv(tpl.Spec.Env, overrides) {
		if env.Name == "VNC_PW" || env.Name == "VNC_PASSWORD" {
			return false
		}
	}
	return true
}

// ensureKasmCredentials creates (create-only) the resolver copy and keeps
// the pod-namespace copy in sync with it. The password is generated once
// and never rotated by the operator: rotating means deleting the
// resolver copy and rolling the workload.
func (r *WorkspaceReconciler) ensureKasmCredentials(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	if !kasmPasswordGenerated(ws, tpl) {
		return nil
	}

	var password string
	resolver := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: kasmSecretName(ws)}, resolver)
	switch {
	case apierrors.IsNotFound(err):
		if password, err = randomKasmPassword(); err != nil {
			return err
		}
		resolver = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kasmSecretName(ws),
				Namespace: ws.Namespace,
				Labels:    workspaceLabels(ws),
			},
			StringData: map[string]string{"password": password},
		}
		if err := r.setOwnerIfLocal(ws, resolver); err != nil {
			return fmt.Errorf("setting owner on kasm secret: %w", err)
		}
		if err := r.Create(ctx, resolver); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("creating kasm secret %s: %w", kasmSecretName(ws), err)
			}
			// Lost a create race: the existing password wins.
			if err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: kasmSecretName(ws)}, resolver); err != nil {
				return fmt.Errorf("re-fetching kasm secret: %w", err)
			}
			password = secretPassword(resolver)
		}
	case err != nil:
		return fmt.Errorf("fetching kasm secret %s: %w", kasmSecretName(ws), err)
	default:
		password = secretPassword(resolver)
	}

	// Pod-side copy: same name as the workload so the teardown sweep
	// catches it. When the workspace is unplaced both copies live in the
	// CR namespace under different names — harmless.
	key := computeKey(ws)
	podCopy := &corev1.Secret{}
	err = r.Get(ctx, key, podCopy)
	switch {
	case apierrors.IsNotFound(err):
		podCopy = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Labels:    workspaceLabels(ws),
			},
			StringData: map[string]string{"password": password},
		}
		if err := r.setOwnerIfLocal(ws, podCopy); err != nil {
			return fmt.Errorf("setting owner on kasm pod secret: %w", err)
		}
		if err := r.Create(ctx, podCopy); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating kasm pod secret %s: %w", key.Name, err)
		}
	case err != nil:
		return fmt.Errorf("fetching kasm pod secret %s: %w", key.Name, err)
	case secretPassword(podCopy) != password:
		// Drift (e.g. manual rotation of the resolver copy): converge on
		// the resolver copy — what connect uses must be what the pod got.
		podCopy.Data = nil
		podCopy.StringData = map[string]string{"password": password}
		if err := r.Update(ctx, podCopy); err != nil {
			return fmt.Errorf("syncing kasm pod secret %s: %w", key.Name, err)
		}
	}
	return nil
}

// kasmEnv is the VNC_PW injection for generated-password workspaces.
func kasmEnv(ws *waasv1alpha1.Workspace) corev1.EnvVar {
	return corev1.EnvVar{
		Name: "VNC_PW",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: computeName(ws)},
				Key:                  "password",
			},
		},
	}
}

// secretPassword reads the password whether the server materialized
// StringData into Data already or not (the fake test client does not).
func secretPassword(s *corev1.Secret) string {
	if v, ok := s.Data["password"]; ok {
		return string(v)
	}
	return s.StringData["password"]
}

func randomKasmPassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating kasm password: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
