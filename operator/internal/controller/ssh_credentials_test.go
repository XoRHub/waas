package controller

import (
	"context"
	"testing"

	"golang.org/x/crypto/ssh"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// sshTemplate serves ssh AND vnc, like dev-ssh: the keypair mechanism
// must coexist with the desktop password one on a single workspace.
func sshTemplate() *waasv1alpha1.WorkspaceTemplate {
	tpl := linuxTemplate()
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "ssh", Port: 2222, Default: true},
		{Name: "vnc", Port: 5901},
	}
	return tpl
}

func TestSSHCredentialsGeneratedAndWired(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, sshTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// Resolver copy next to the CR: both keypair halves, owner-ref'd,
	// and real key material — guacd and sshd must be able to parse it.
	resolver := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-ssh-marc"}, resolver); err != nil {
		t.Fatalf("expected the ssh resolver secret: %v", err)
	}
	if len(resolver.OwnerReferences) != 1 {
		t.Fatal("resolver secret must be owner-referenced to the workspace")
	}
	signer, err := ssh.ParsePrivateKey([]byte(sshSecretValue(resolver, sshPrivateKeyKey)))
	if err != nil {
		t.Fatalf("private-key must be a valid OpenSSH PEM key: %v", err)
	}
	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(sshSecretValue(resolver, sshPublicKeyKey)))
	if err != nil {
		t.Fatalf("public-key must be a valid authorized_keys line: %v", err)
	}
	if string(ssh.MarshalAuthorizedKey(signer.PublicKey())) != string(ssh.MarshalAuthorizedKey(parsedPub)) {
		t.Fatal("the two Secret keys must be halves of the SAME keypair")
	}

	// Pod copy: suffixed name, workspace labels, public key ONLY.
	podCopy := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-ssh"}, podCopy); err != nil {
		t.Fatalf("expected the pod-namespace ssh secret: %v", err)
	}
	if podCopy.Labels[labelWorkspace] != ws.Name {
		t.Fatal("pod copy must carry the workspace content labels")
	}
	if sshSecretValue(podCopy, sshPrivateKeyKey) != "" {
		t.Fatal("pod copy must hold the public key ONLY, never the private key")
	}
	if sshSecretValue(podCopy, sshPublicKeyKey) != sshSecretValue(resolver, sshPublicKeyKey) {
		t.Fatal("pod copy must hold the resolver's public key")
	}

	// Pod wiring: env pointing at the mount, sshd turned on (no explicit
	// WAAS_SSH_ENABLED in the template), read-only volume+mount. The
	// desktop password mechanism fires independently for vnc.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("expected desktop deployment: %v", err)
	}
	env := map[string]string{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["WAAS_SSH_AUTHORIZED_KEYS_FILE"] != "/etc/waas/credentials/ssh/authorized_keys" {
		t.Fatalf("expected the authorized-keys file injection, got %q", env["WAAS_SSH_AUTHORIZED_KEYS_FILE"])
	}
	if env["WAAS_SSH_ENABLED"] != "1" {
		t.Fatalf("declaring ssh must enable sshd when the template is silent, got %q", env["WAAS_SSH_ENABLED"])
	}
	if _, ok := env["WAAS_DESKTOP_PASSWORD"]; !ok {
		t.Fatal("the vnc password mechanism must fire alongside ssh")
	}
	var mount *corev1.VolumeMount
	for i, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == sshCredentialsVolume {
			mount = &dep.Spec.Template.Spec.Containers[0].VolumeMounts[i]
		}
	}
	if mount == nil || !mount.ReadOnly || mount.MountPath != sshCredentialsMountPath {
		t.Fatalf("expected a read-only ssh credentials mount, got %+v", mount)
	}
	var volume *corev1.Volume
	for i, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == sshCredentialsVolume {
			volume = &dep.Spec.Template.Spec.Volumes[i]
		}
	}
	if volume == nil || volume.Secret == nil || volume.Secret.SecretName != "ws-marc-ssh" {
		t.Fatalf("expected the volume to read the suffixed pod copy, got %+v", volume)
	}
	if len(volume.Secret.Items) != 1 || volume.Secret.Items[0].Key != sshPublicKeyKey || volume.Secret.Items[0].Path != sshAuthorizedKeysFile {
		t.Fatalf("volume must project public-key as authorized_keys, got %+v", volume.Secret.Items)
	}

	// Idempotent: a second pass must not rotate the keypair.
	private := sshSecretValue(resolver, sshPrivateKeyKey)
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-ssh-marc"}, resolver); err != nil {
		t.Fatal(err)
	}
	if sshSecretValue(resolver, sshPrivateKeyKey) != private {
		t.Fatal("reconcile must not rotate the generated keypair")
	}

	// Drift on the pod copy — including a smuggled private key — must
	// converge back on public-key-only.
	podCopy.Data = nil
	podCopy.StringData = map[string]string{
		sshPublicKeyKey:  "tampered",
		sshPrivateKeyKey: "smuggled",
	}
	if err := c.Update(ctx, podCopy); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-ssh"}, podCopy); err != nil {
		t.Fatal(err)
	}
	if sshSecretValue(podCopy, sshPublicKeyKey) != sshSecretValue(resolver, sshPublicKeyKey) || sshSecretValue(podCopy, sshPrivateKeyKey) != "" {
		t.Fatal("pod copy must converge on the resolver's public key only")
	}
}

// An explicit WAAS_SSH_ENABLED=0 is the admin's call: keys are still
// generated (the predicate only looks at credential sources) but the
// injection must not overwrite the opt-out.
func TestSSHCredentialsPreserveExplicitEnabledOff(t *testing.T) {
	tpl := sshTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_SSH_ENABLED", Value: "0"}}
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "waas-ssh-marc"}, &corev1.Secret{}); err != nil {
		t.Fatalf("keys must still be generated with an explicit enable toggle: %v", err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		seen[e.Name]++
		if e.Name == "WAAS_SSH_ENABLED" && e.Value != "0" {
			t.Fatalf("explicit WAAS_SSH_ENABLED=0 must be preserved, got %q", e.Value)
		}
	}
	if seen["WAAS_SSH_ENABLED"] != 1 {
		t.Fatalf("expected exactly one WAAS_SSH_ENABLED entry, got %d", seen["WAAS_SSH_ENABLED"])
	}
	if seen["WAAS_SSH_AUTHORIZED_KEYS_FILE"] != 1 {
		t.Fatal("the authorized-keys file must still be wired")
	}
}

// The "explicit source wins" matrix: any admin-provided credential path
// switches the mechanism off entirely.
func TestSSHCredentialsSkippedWhenExplicit(t *testing.T) {
	for name, mutate := range map[string]func(*waasv1alpha1.WorkspaceTemplate, *waasv1alpha1.Workspace){
		"credentialsSecretRef": func(tpl *waasv1alpha1.WorkspaceTemplate, _ *waasv1alpha1.Workspace) {
			tpl.Spec.Protocols[0].CredentialsSecretRef = "ssh-creds"
		},
		"template WAAS_SSH_AUTHORIZED_KEYS": func(tpl *waasv1alpha1.WorkspaceTemplate, _ *waasv1alpha1.Workspace) {
			tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_SSH_AUTHORIZED_KEYS", Value: "ssh-ed25519 AAAA admin"}}
		},
		"template WAAS_SSH_AUTHORIZED_KEYS_FILE": func(tpl *waasv1alpha1.WorkspaceTemplate, _ *waasv1alpha1.Workspace) {
			tpl.Spec.Env = []corev1.EnvVar{{Name: "WAAS_SSH_AUTHORIZED_KEYS_FILE", Value: "/mnt/keys/authorized_keys"}}
		},
		"override WAAS_SSH_AUTHORIZED_KEYS": func(_ *waasv1alpha1.WorkspaceTemplate, ws *waasv1alpha1.Workspace) {
			ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
				Env: []corev1.EnvVar{{Name: "WAAS_SSH_AUTHORIZED_KEYS", Value: "ssh-ed25519 AAAA user"}},
			}
		},
		"no ssh protocol": func(tpl *waasv1alpha1.WorkspaceTemplate, _ *waasv1alpha1.Workspace) {
			tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{{Name: "vnc", Port: 5901, Default: true}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tpl, ws := sshTemplate(), workspace()
			mutate(tpl, ws)
			r, c := newFixture(t, tpl, ws)

			reconcile(t, r, ws)

			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "waas-ssh-marc"}, &corev1.Secret{}); err == nil {
				t.Fatal("no keypair may be generated when an explicit source exists")
			}
			dep := &appsv1.Deployment{}
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
				t.Fatal(err)
			}
			for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
				if e.Name == "WAAS_SSH_AUTHORIZED_KEYS_FILE" && e.Value == "/etc/waas/credentials/ssh/authorized_keys" {
					t.Fatal("the generated-keys wiring must not be injected")
				}
			}
			for _, v := range dep.Spec.Template.Spec.Volumes {
				if v.Name == sshCredentialsVolume {
					t.Fatal("the ssh credentials volume must not be mounted")
				}
			}
		})
	}
}

// The suffixed pod copy is the one content object the name-based
// teardown sweep misses: the finalizer must delete it explicitly.
func TestSSHPodCopySweptOnTeardown(t *testing.T) {
	tpl := sshTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-{user}"}
	ws := placedWorkspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	podCopyKey := types.NamespacedName{Namespace: "waas-alice", Name: "cad-station-ssh"}
	if err := c.Get(ctx, podCopyKey, &corev1.Secret{}); err != nil {
		t.Fatalf("expected the pod copy in the placed namespace: %v", err)
	}

	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, podCopyKey, &corev1.Secret{}); err == nil {
		t.Fatal("teardown must delete the suffixed ssh pod copy")
	}
}
