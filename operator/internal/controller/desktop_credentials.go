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

// Generated desktop (vnc/rdp) credentials.
//
// The vnc/rdp sibling of the KasmVNC mechanism (kasm_credentials.go):
// when a template serves vnc or rdp WITHOUT an explicit password source
// (template/override env, credentialsSecretRef), the operator generates
// a random per-workspace password so that no two workspaces share one
// and nothing secret lands in a CR. waas-images containers authenticate
// both protocols against ONE VncAuth file (xrdp's libvnc bridge forwards
// the RDP password to Xvnc), so a workspace gets AT MOST ONE generated
// password whether it exposes vnc, rdp, or both. It materializes as TWO
// Secrets, exactly like the kasm mechanism:
//
//   - waas-desktop-<workspace> in the CR namespace: the copy the
//     api-server resolves at connect time. Owner-referenced to the
//     Workspace, so it is garbage-collected with the CR.
//   - <workloadName> in the pod namespace: what the pod's
//     WAAS_DESKTOP_PASSWORD secretKeyRef reads. Same name and key as
//     the kasm pod copy — safe
//     because the two mechanisms are mutually exclusive (see
//     desktopPasswordGenerated), and required so the teardown
//     finalizer's name-based sweep deletes it and the namespace janitor
//     counts it as content.
//
// The template admission webhook is the primary guard against the
// collision: kasmvnc is an exclusive protocol, so a template mixing it
// with vnc/rdp is rejected at create/update. The runtime yield below
// stays as defense-in-depth for grandfathered templates admitted before
// that rule (admission never re-checks objects already stored).

// desktopSecretName is the resolver-side copy, next to the Workspace CR.
// Name shared with the api-server via v1alpha1.DesktopSecretName.
func desktopSecretName(ws *waasv1alpha1.Workspace) string {
	return waasv1alpha1.DesktopSecretName(ws.Name)
}

// desktopPasswordGenerated says whether THIS workspace runs with an
// operator-generated vnc/rdp password. One answer for the whole
// workspace, never per protocol: the container has a single session
// password. Must stay aligned with the api-server's fallback (password
// still empty after every explicit source → read the generated Secret).
func desktopPasswordGenerated(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		return false
	}
	// Mutually exclusive with the kasm mechanism: they inject different
	// env names (WAAS_DESKTOP_PASSWORD here, kasmweb's VNC_PW there) but
	// share the pod-copy Secret name, so only one may generate. The
	// webhook now rejects kasmvnc+vnc/rdp templates at admission, but a
	// template stored before that rule can still combine them — kasm
	// keeps winning here for those.
	if kasmPasswordGenerated(ws, tpl) {
		return false
	}
	eligible := false
	for _, p := range tpl.Spec.EffectiveProtocols() {
		if (p.Name == string(waasv1alpha1.ProtocolVNC) || p.Name == string(waasv1alpha1.ProtocolRDP)) &&
			p.CredentialsSecretRef == "" {
			eligible = true
			break
		}
	}
	if !eligible {
		return false
	}
	var overrides []corev1.EnvVar
	if ws.Spec.Overrides != nil {
		overrides = ws.Spec.Overrides.Env
	}
	for _, env := range mergeEnv(tpl.Spec.Env, overrides) {
		if env.Name == "WAAS_DESKTOP_PASSWORD" {
			return false
		}
	}
	return true
}

// ensureDesktopCredentials creates (create-only) the resolver copy and
// keeps the pod-namespace copy in sync with it. The password is generated
// once and never rotated by the operator: rotating means deleting the
// resolver copy and rolling the workload.
func (r *WorkspaceReconciler) ensureDesktopCredentials(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	if !desktopPasswordGenerated(ws, tpl) {
		return nil
	}

	var password string
	resolver := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: desktopSecretName(ws)}, resolver)
	switch {
	case apierrors.IsNotFound(err):
		if password, err = randomDesktopPassword(); err != nil {
			return err
		}
		resolver = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      desktopSecretName(ws),
				Namespace: ws.Namespace,
				Labels:    workspaceLabels(ws),
			},
			StringData: map[string]string{"password": password},
		}
		if err := r.setOwnerIfLocal(ws, resolver); err != nil {
			return fmt.Errorf("setting owner on desktop secret: %w", err)
		}
		if err := r.Create(ctx, resolver); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("creating desktop secret %s: %w", desktopSecretName(ws), err)
			}
			// Lost a create race: the existing password wins.
			if err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: desktopSecretName(ws)}, resolver); err != nil {
				return fmt.Errorf("re-fetching desktop secret: %w", err)
			}
			password = secretPassword(resolver)
		}
	case err != nil:
		return fmt.Errorf("fetching desktop secret %s: %w", desktopSecretName(ws), err)
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
			return fmt.Errorf("setting owner on desktop pod secret: %w", err)
		}
		if err := r.Create(ctx, podCopy); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating desktop pod secret %s: %w", key.Name, err)
		}
	case err != nil:
		return fmt.Errorf("fetching desktop pod secret %s: %w", key.Name, err)
	case secretPassword(podCopy) != password:
		// Drift (e.g. manual rotation of the resolver copy): converge on
		// the resolver copy — what connect uses must be what the pod got.
		podCopy.Data = nil
		podCopy.StringData = map[string]string{"password": password}
		if err := r.Update(ctx, podCopy); err != nil {
			return fmt.Errorf("syncing desktop pod secret %s: %w", key.Name, err)
		}
	}
	return nil
}

// desktopCredentialsEnv is the WAAS_DESKTOP_PASSWORD injection for
// generated-password vnc/rdp workspaces. Same wiring as kasmEnv — the
// pod copy shares the workload name — but NOT the same env name: kasm
// speaks the kasmweb images' vocabulary (VNC_PW), this speaks the
// waas-images WAAS_ contract. Kept as its own function so each
// mechanism reads standalone.
func desktopCredentialsEnv(ws *waasv1alpha1.Workspace) corev1.EnvVar {
	return corev1.EnvVar{
		Name: "WAAS_DESKTOP_PASSWORD",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: computeName(ws)},
				Key:                  "password",
			},
		},
	}
}

// randomDesktopPassword matches randomKasmPassword's format (crypto/rand,
// 16 bytes hex); duplicated so the two mechanisms stay independent.
func randomDesktopPassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating desktop password: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
