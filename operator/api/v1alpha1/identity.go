package v1alpha1

// Identity annotations stamped on Workspace objects. They carry the
// IdP-derived (OIDC) identity used for policy resolution and are part of
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

	// AnnotationGroups is the comma-separated list of IdP (OIDC) groups
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

	// AnnotationDeleteHome, stamped by the api-server just before a
	// workspace deletion, carries the user's EXPLICIT choice to delete the
	// home volume with the workspace. Absent (the default) the finalizer
	// detaches the volume instead: it survives as a retained volume, owned
	// by the user and still counted against their storage quota. No volume
	// is ever deleted without this explicit opt-in.
	AnnotationDeleteHome = "waas.xorhub.io/delete-home"

	// AnnotationReloadRequestedAt is the RFC3339 timestamp of a manual
	// reload request (POST /workspaces/{id}/reload): the user asks for ONE
	// immediate convergence boundary — the running workload restarts on
	// the up-to-date pod template (docs/adr/0001) and the operator removes
	// the annotation once consumed. Deliberately disjoint from spec.paused
	// and AnnotationManualStateAt: a reload must never disturb the pause
	// intent or the schedule conflict resolution (rule B).
	AnnotationReloadRequestedAt = "waas.xorhub.io/reload-requested-at"
)

// Platform labels stamped on every object the operator manages (workloads,
// services, PVCs, bootstrapped namespaces). Shared between the operator
// (stamping), the webhook (namespace-ownership enforcement) and the
// api-server; user metadata can never carry these keys (pkg/metakeys).
const (
	// LabelManagedBy marks operator-managed objects
	// (app.kubernetes.io/managed-by = waas-operator).
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// LabelWorkspace carries the owning Workspace CR name.
	LabelWorkspace = "waas.xorhub.io/workspace"

	// LabelWorkspaceNamespace carries the namespace of the Workspace CR
	// itself (the platform namespace). Needed on cross-namespace workloads,
	// where owner references are illegal: the controller maps watch events
	// back to the CR through this label.
	LabelWorkspaceNamespace = "waas.xorhub.io/workspace-namespace"

	// LabelOwner carries the platform user (UUID) owning the object. On a
	// bootstrapped namespace it is the ownership proof the webhook checks
	// before letting a workspace target it.
	LabelOwner = "waas.xorhub.io/owner"

	// ManagerName is the LabelManagedBy value.
	ManagerName = "waas-operator"

	// LabelRetained marks a home PVC detached from a deleted workspace
	// ("true"). The volume stays the user's property (LabelOwner) and
	// keeps counting against their storage quota until deleted or adopted
	// by a new workspace (spec.homeVolumeName).
	LabelRetained = "waas.xorhub.io/retained"

	// AnnotationOriginWorkspace preserves, on a retained volume, the
	// display name of the workspace it came from (provenance in the
	// volumes dashboards).
	AnnotationOriginWorkspace = "waas.xorhub.io/origin-workspace"

	// AnnotationRetainedAt is the RFC3339 timestamp of the detachment.
	AnnotationRetainedAt = "waas.xorhub.io/retained-at"
)
