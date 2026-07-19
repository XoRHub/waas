package v1alpha1

// Resolver-side names of the operator-generated credential Secrets that
// live next to the Workspace CR — the shared half of the contract
// between the operator (which generates them, internal/controller/
// {kasm,desktop}_credentials.go) and the api-server (which resolves the
// password at connect time, workspace_service.go). Same doctrine as
// SSHSecretName (ssh_credentials.go): ONE name serves both sides
// instead of two comment-aligned copies. The generation predicates stay
// operator-side: the api-server only ever falls back to these Secrets
// when every explicit credential source came up empty.

// KasmSecretName is the resolver-side copy of a workspace's generated
// KasmVNC password (key: "password"). The pod-namespace copy is named
// after the workload and read by the pod's VNC_PW secretKeyRef.
func KasmSecretName(workspaceName string) string { return "waas-kasm-" + workspaceName }

// DesktopSecretName is the vnc/rdp sibling of KasmSecretName — one
// session password for the whole workspace, never per protocol.
func DesktopSecretName(workspaceName string) string { return "waas-desktop-" + workspaceName }
