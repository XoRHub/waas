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
)
