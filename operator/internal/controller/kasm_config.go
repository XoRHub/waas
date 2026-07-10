package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/kasmcfg"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// User-level KasmVNC configuration (WorkspaceTemplate.spec.kasmvncConfig)
// plus policy-derived clipboard enforcement.
//
// What WaaS writes into ~/.vnc/kasmvnc.yaml is TWO layers merged:
//   1. the admin's opaque kasmvncConfig (convenience config), then
//   2. the clipboard DLP keys derived from WorkspacePolicy.Clipboard
//      (the security authority), stamped last so denying clipboard in
//      the policy actually disables copy/paste in the container instead
//      of only greying out a button (docs/studies/08-prompt-feature11-*).
// Everything else the admin wrote is preserved.
//
// WaaS does NOT add a base/defaults layer of its own. KasmVNC already
// deep-merges this user file over the image's shipped defaults
// (/usr/share/kasmvnc/kasmvnc_defaults.yaml < /etc/kasmvnc/kasmvnc.yaml <
// ~/.vnc/kasmvnc.yaml), verified live: a partial user file inherits every
// unspecified key (resolution, encoding, ssl…) from the image defaults.
// Re-baking those defaults into a WaaS constant would freeze them against
// future image versions, so the effective "base" is the image's own file,
// not ours. Reference:
// https://kasmweb.com/kasmvnc/docs/latest/configuration.html
//
// The result is materialized as a per-workspace ConfigMap in the pod's
// namespace (mounted with subPath at <home>/.vnc/kasmvnc.yaml; the .vnc
// directory itself is an operator-managed emptyDir so it is writable for
// KasmVNC's runtime artifacts — self.pem lives there — whatever uid the
// image runs as; see buildPodTemplate). The ConfigMap shares the
// workload's name: the teardown finalizer's name-based sweep and the
// janitor's content check cover it with zero extra code. subPath mounts
// never refresh in place, so the content hash is stamped on the pod
// template (annotationKasmConfigHash) — a policy or template change rolls
// the workload.

const (
	kasmConfigKey            = "kasmvnc.yaml"
	kasmConfigVolume         = "kasmvnc-config"
	kasmVncDirVolume         = "kasmvnc-dir"
	annotationKasmConfigHash = "waas.xorhub.io/kasmvnc-config-hash"
)

func kasmConfigHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

// templateHasKasmVNC reports whether any of the template's protocols is
// the web-native KasmVNC endpoint (the only one this config applies to).
func templateHasKasmVNC(tpl *waasv1alpha1.WorkspaceTemplate) bool {
	for _, p := range tpl.Spec.Protocols {
		if p.Name == string(waasv1alpha1.ProtocolKasmVNC) {
			return true
		}
	}
	return false
}

// kasmClipboardGrant resolves the workspace owner's clipboard rights.
// Container-level DLP can only enforce ONE policy per workload, so it
// follows the owner (personal kasmvnc namespaces make owner == user in
// practice; a shared workspace enforces the owner's policy for everyone).
// Resolution failure fails closed: no policy match means clipboard off.
func (r *WorkspaceReconciler) kasmClipboardGrant(ctx context.Context, ws *waasv1alpha1.Workspace) (copyAllowed, pasteAllowed bool) {
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(ws.Namespace)); err != nil {
		return false, false
	}
	pol, _, denial := policy.Resolve(policies.Items, policy.IdentityOf(ws))
	if denial != nil {
		return false, false
	}
	return policy.ClipboardOf(pol)
}

// applyClipboardPolicy merges the policy's clipboard decision into the
// admin's opaque kasmvnc.yaml, preserving every other directive. The
// policy is authoritative, so allow_client_to_override_kasm_server_settings
// is forced off — KasmVNC's DLP directives are ignored otherwise and the
// client could re-enable clipboard at connect time. The stamped paths come
// from kasmcfg.ManagedClipboardKeyPaths (single source shared with the
// webhook/api-server rejection), paired here with their values IN THAT
// ORDER: clipboard copy, clipboard paste, client-override-off.
func applyClipboardPolicy(rawConfig string, copyAllowed, pasteAllowed bool) (string, error) {
	root := map[string]any{}
	if strings.TrimSpace(rawConfig) != "" {
		if err := yaml.Unmarshal([]byte(rawConfig), &root); err != nil {
			return "", fmt.Errorf("parsing kasmvnc config: %w", err)
		}
		if root == nil {
			root = map[string]any{}
		}
	}
	values := []any{copyAllowed, pasteAllowed, false}
	for i, path := range kasmcfg.ManagedClipboardKeyPaths {
		setNested(root, values[i], path...)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("serializing kasmvnc config: %w", err)
	}
	return string(out), nil
}

// setNested assigns value at the nested map path, creating intermediate
// maps as needed. A key whose existing value is not a map is overwritten
// with a fresh map so a scalar in the admin's config can't shadow the
// enforced sub-tree.
func setNested(root map[string]any, value any, keys ...string) {
	m := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = value
}

// ensureKasmConfig keeps the per-workspace ConfigMap converged on the
// effective KasmVNC config (admin template + policy clipboard
// enforcement), and removes it for non-kasmvnc workspaces.
func (r *WorkspaceReconciler) ensureKasmConfig(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	key := computeKey(ws)
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, key, existing)

	if !templateHasKasmVNC(tpl) {
		// Not a kasmvnc workspace: there is nothing to enforce and no
		// kasmvnc.yaml to mount; drop any stale ConfigMap.
		if err == nil {
			if derr := r.Delete(ctx, existing); derr != nil && !apierrors.IsNotFound(derr) {
				return fmt.Errorf("deleting kasmvnc config %s: %w", key.Name, derr)
			}
		}
		return nil
	}

	copyAllowed, pasteAllowed := r.kasmClipboardGrant(ctx, ws)
	content, cerr := applyClipboardPolicy(tpl.Spec.KasmVNCConfig, copyAllowed, pasteAllowed)
	if cerr != nil {
		return fmt.Errorf("building kasmvnc config %s: %w", key.Name, cerr)
	}

	switch {
	case apierrors.IsNotFound(err):
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Labels:    workspaceLabels(ws),
			},
			Data: map[string]string{kasmConfigKey: content},
		}
		if err := r.setOwnerIfLocal(ws, cm); err != nil {
			return fmt.Errorf("setting owner on kasmvnc config: %w", err)
		}
		if err := r.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating kasmvnc config %s: %w", key.Name, err)
		}
	case err != nil:
		return fmt.Errorf("fetching kasmvnc config %s: %w", key.Name, err)
	case existing.Data[kasmConfigKey] != content:
		existing.Data = map[string]string{kasmConfigKey: content}
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("syncing kasmvnc config %s: %w", key.Name, err)
		}
	}
	return nil
}
