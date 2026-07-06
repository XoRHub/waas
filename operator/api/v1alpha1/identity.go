package v1alpha1

// Identity annotations stamped on Workspace objects. They carry the
// Authentik-derived identity used for policy resolution and are part of
// the trusted-writer contract: the admission webhook only accepts them
// from the api-server's ServiceAccount and freezes them afterwards
// (immutable on update, whoever the caller is). For any other creator
// (direct kubectl), the webhook overrides identity with the Kubernetes
// request userInfo instead — these annotations are then ignored.
const (
	// AnnotationUsername is the human identity (OIDC preferred_username /
	// sub) of the workspace owner. spec.owner stays the opaque platform
	// UUID; policy User subjects match either.
	AnnotationUsername = "waas.xorhub.io/username"

	// AnnotationGroups is the comma-separated list of Authentik groups
	// the owner belonged to at creation time.
	AnnotationGroups = "waas.xorhub.io/groups"

	// AnnotationRole is the platform role ("admin" or "user") of the
	// owner at creation time. Platform admins may override any template
	// field; the webhook trusts this only from trusted writers, like the
	// other identity annotations.
	AnnotationRole = "waas.xorhub.io/role"

	// AnnotationManualStateAt is the RFC3339 timestamp of the last manual
	// pause/resume, stamped by the api-server. The schedule evaluator uses
	// it to apply conflict rule B (a manual action wins until the next
	// opposite scheduled edge). Not part of the trusted-identity contract.
	AnnotationManualStateAt = "waas.xorhub.io/manual-state-at"
)
