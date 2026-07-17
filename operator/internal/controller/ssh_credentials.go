package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"golang.org/x/crypto/ssh"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// Generated SSH credentials.
//
// The ssh sibling of the desktop/kasm password mechanisms
// (desktop_credentials.go, kasm_credentials.go): when a template
// declares the ssh protocol WITHOUT an explicit credential source
// (credentialsSecretRef, or WAAS_SSH_AUTHORIZED_KEYS(_FILE) in
// template/override env), the operator generates a per-workspace
// ed25519 keypair so that ssh works with zero template configuration
// and nothing secret lands in a CR. Two Secrets, same two-copy pattern
// as the siblings — with one asymmetry the passwords don't have:
//
//   - waas-ssh-<workspace> in the CR namespace (the resolver copy the
//     api-server reads at connect time, owner-ref'd): private-key
//     (OpenSSH PEM, mapped verbatim into guacd's vocabulary) AND
//     public-key (one authorized_keys line).
//   - <workloadName>-ssh in the pod namespace: public-key ONLY — the
//     private key never exists in the pod's namespace. Suffixed, unlike
//     the password pod copies, because ssh coexists with vnc/rdp on one
//     workspace (the desktop/kasm pair is mutually exclusive and can
//     share the bare workload name); the name-based teardown sweep
//     therefore misses it and teardownPlacement deletes it explicitly.
//
// The keypair serves the PORTAL path only: guacd holds the private key.
// Pod-to-pod ssh still needs an admin-shipped Secret — the resolver
// copy is per-workspace and not referenceable from template volumes.

const (
	sshPrivateKeyKey = "private-key"
	sshPublicKeyKey  = "public-key"

	sshCredentialsVolume = "ssh-credentials"
	// Outside the home PVC on purpose; the images' entrypoint copies the
	// file into tmpfs and re-chmods it, so 0444 readability is enough.
	sshCredentialsMountPath = "/etc/waas/credentials/ssh"
	sshAuthorizedKeysFile   = "authorized_keys"
)

// sshPodSecretName is the pod-namespace, public-key-only copy.
func sshPodSecretName(ws *waasv1alpha1.Workspace) string { return computeName(ws) + "-ssh" }

// sshKeyGenerated adapts the shared predicate (v1alpha1.SSHKeyGenerated,
// also driving the api-server's connect-time fallback) to the
// reconciler's merged-env view.
func sshKeyGenerated(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	var overrides []corev1.EnvVar
	if ws.Spec.Overrides != nil {
		overrides = ws.Spec.Overrides.Env
	}
	return waasv1alpha1.SSHKeyGenerated(tpl, mergeEnv(tpl.Spec.Env, overrides))
}

// ensureSSHCredentials creates (create-only) the resolver copy and keeps
// the pod-namespace public-key copy in sync with it. The keypair is
// generated once and never rotated by the operator: rotating means
// deleting the resolver copy and rolling the workload.
func (r *WorkspaceReconciler) ensureSSHCredentials(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	if !sshKeyGenerated(ws, tpl) {
		return nil
	}

	name := waasv1alpha1.SSHSecretName(ws.Name)
	var publicKey string
	resolver := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: name}, resolver)
	switch {
	case apierrors.IsNotFound(err):
		privatePEM, authorizedKey, err := generateSSHKeypair()
		if err != nil {
			return err
		}
		publicKey = authorizedKey
		resolver = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ws.Namespace,
				Labels:    workspaceLabels(ws),
			},
			StringData: map[string]string{
				sshPrivateKeyKey: privatePEM,
				sshPublicKeyKey:  authorizedKey,
			},
		}
		if err := r.setOwnerIfLocal(ws, resolver); err != nil {
			return fmt.Errorf("setting owner on ssh secret: %w", err)
		}
		if err := r.Create(ctx, resolver); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("creating ssh secret %s: %w", name, err)
			}
			// Lost a create race: the existing keypair wins.
			if err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: name}, resolver); err != nil {
				return fmt.Errorf("re-fetching ssh secret: %w", err)
			}
			publicKey = sshSecretValue(resolver, sshPublicKeyKey)
		}
	case err != nil:
		return fmt.Errorf("fetching ssh secret %s: %w", name, err)
	default:
		publicKey = sshSecretValue(resolver, sshPublicKeyKey)
	}

	// Pod-side copy, public-key only. When the workspace is unplaced both
	// copies live in the CR namespace under different names — harmless.
	key := computeKey(ws)
	key.Name = sshPodSecretName(ws)
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
			StringData: map[string]string{sshPublicKeyKey: publicKey},
		}
		if err := r.setOwnerIfLocal(ws, podCopy); err != nil {
			return fmt.Errorf("setting owner on ssh pod secret: %w", err)
		}
		if err := r.Create(ctx, podCopy); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating ssh pod secret %s: %w", key.Name, err)
		}
	case err != nil:
		return fmt.Errorf("fetching ssh pod secret %s: %w", key.Name, err)
	case sshSecretValue(podCopy, sshPublicKeyKey) != publicKey || sshSecretValue(podCopy, sshPrivateKeyKey) != "":
		// Drift (manual rotation of the resolver copy, or a stray key such
		// as a copied private-key): converge on public-key-only, matching
		// what guacd will authenticate with.
		podCopy.Data = nil
		podCopy.StringData = map[string]string{sshPublicKeyKey: publicKey}
		if err := r.Update(ctx, podCopy); err != nil {
			return fmt.Errorf("syncing ssh pod secret %s: %w", key.Name, err)
		}
	}
	return nil
}

// sshCredentialsEnv is the pod wiring for a generated keypair: point the
// entrypoint at the mounted authorized_keys and, when the template does
// not take a position on WAAS_SSH_ENABLED, turn sshd on — declaring the
// ssh protocol IS the intent signal, while an explicit "0" is preserved
// (sshd stays off, the keys sit unused).
func sshCredentialsEnv(mergedEnv []corev1.EnvVar) []corev1.EnvVar {
	out := []corev1.EnvVar{{
		Name:  "WAAS_SSH_AUTHORIZED_KEYS_FILE",
		Value: sshCredentialsMountPath + "/" + sshAuthorizedKeysFile,
	}}
	for _, env := range mergedEnv {
		if env.Name == "WAAS_SSH_ENABLED" {
			return out
		}
	}
	return append(out, corev1.EnvVar{Name: "WAAS_SSH_ENABLED", Value: "1"})
}

// sshSecretValue reads a key whether the server materialized StringData
// into Data already or not (the fake test client does not) — the ssh
// sibling of secretPassword.
func sshSecretValue(s *corev1.Secret, key string) string {
	if v, ok := s.Data[key]; ok {
		return string(v)
	}
	return s.StringData[key]
}

// generateSSHKeypair returns a fresh ed25519 keypair as (OpenSSH PEM
// private key, one-line authorized_keys public key) — the exact formats
// guacd and sshd consume, verified parseable by the unit tests.
func generateSSHKeypair() (privatePEM, authorizedKey string, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generating ssh keypair: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return "", "", fmt.Errorf("marshaling ssh private key: %w", err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", "", fmt.Errorf("marshaling ssh public key: %w", err)
	}
	return string(pem.EncodeToMemory(block)), string(ssh.MarshalAuthorizedKey(sshPublic)), nil
}
