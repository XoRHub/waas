package v1alpha1

import corev1 "k8s.io/api/core/v1"

// Generated SSH credentials — the shared half of the contract between
// the operator (which generates the keypair, ssh_credentials.go) and
// the api-server (which resolves the private key at connect time,
// workspace_service.go). Living here, ONE predicate and ONE Secret name
// serve both sides — the desktop/kasm password mechanisms share their
// names the same way (credential_secrets.go), only their generation
// predicates remain operator-side.

// SSHSecretName is the resolver-side copy of a workspace's generated
// ssh keypair, next to the Workspace CR. Keys: "private-key" (OpenSSH
// PEM, guacd's credentials vocabulary) and "public-key" (one
// authorized_keys line). The pod-namespace copy carries "public-key"
// only — the private key never exists in the pod's namespace.
func SSHSecretName(workspaceName string) string { return "waas-ssh-" + workspaceName }

// SSHKeyGenerated says whether a workspace running this template gets
// an operator-generated ssh keypair: an ssh protocol is declared with
// no credentialsSecretRef, and no explicit authorized-keys source
// appears in the merged env — the same "generate only when nothing
// explicit is provided" doctrine as the desktop and kasm passwords
// (docs/templates-and-protocols.md, precedence level 2).
//
// mergedEnv is the template env with the workspace's admitted overrides
// applied; only NAMES are inspected (an override can shadow a value but
// never remove a name), so passing the two lists concatenated is
// equally correct.
func SSHKeyGenerated(tpl *WorkspaceTemplate, mergedEnv []corev1.EnvVar) bool {
	if tpl.Spec.OS == OSWindows {
		return false
	}
	eligible := false
	for _, p := range tpl.Spec.EffectiveProtocols() {
		if p.Name == string(ProtocolSSH) && p.CredentialsSecretRef == "" {
			eligible = true
			break
		}
	}
	if !eligible {
		return false
	}
	for _, env := range mergedEnv {
		if env.Name == "WAAS_SSH_AUTHORIZED_KEYS" || env.Name == "WAAS_SSH_AUTHORIZED_KEYS_FILE" {
			return false
		}
	}
	return true
}
