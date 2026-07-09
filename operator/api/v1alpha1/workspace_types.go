package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspacePhase is the coarse lifecycle state surfaced to the fleet dashboard.
// +kubebuilder:validation:Enum=Pending;Provisioning;Running;Paused;Stopped;Failed;Terminating
type WorkspacePhase string

const (
	PhasePending      WorkspacePhase = "Pending"
	PhaseProvisioning WorkspacePhase = "Provisioning"
	PhaseRunning      WorkspacePhase = "Running"
	// PhasePaused: the user manually paused the workspace. Compute is
	// scaled to 0 (the Deployment/StatefulSet and all its config are
	// kept), the home volume is retained; resume is a scale back to 1.
	PhasePaused WorkspacePhase = "Paused"
	// PhaseStopped: the workspace is down because of a scheduled downtime
	// window (see spec.schedule), not a manual action. Same scale-to-0
	// mechanism as Paused; the distinction is why it is down.
	PhaseStopped     WorkspacePhase = "Stopped"
	PhaseFailed      WorkspacePhase = "Failed"
	PhaseTerminating WorkspacePhase = "Terminating"
)

// Condition types set on Workspace.
const (
	ConditionReady = "Ready"
	// ConditionTemplateDrifted says the desired configuration changed
	// since this workspace's workload was (re)built — a template edit OR
	// a workspace override update, both feed the fingerprint: the new
	// shape applies at the next scale-up boundary (resume / scheduled
	// uptime) or on manual reload, NEVER silently mid-session (see
	// docs/adr/0001). The UI surfaces it as a clickable "update pending"
	// notice. The name predates override-driven drift and is kept for
	// API compatibility.
	ConditionTemplateDrifted = "TemplateDrifted"
	// ConditionConnectionReady says the desktop server actually LISTENS
	// on the default protocol port (TCP probe by the operator) — pod
	// readiness proves the container is up, not that the desktop
	// accepts connections.
	ConditionConnectionReady = "ConnectionReady"
)

// WorkspaceSpec defines the desired state of a user workspace.
type WorkspaceSpec struct {
	// TemplateRef names the WorkspaceTemplate (same namespace) this
	// workspace is stamped from.
	// +kubebuilder:validation:MinLength=1
	TemplateRef string `json:"templateRef"`

	// Owner identifies the platform user this workspace belongs to (UUID).
	// The operator treats it as an opaque label value; it never reads the DB.
	// +kubebuilder:validation:MinLength=1
	Owner string `json:"owner"`

	// DisplayName is the human-facing workspace name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Paused stops the workspace: compute is deleted, the home volume is
	// kept, and the workspace resumes where it left off when unpaused.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// TargetNamespace is where the workloads (Deployment/Service/PVC)
	// live. Resolved from the template placement pattern at creation and
	// IMMUTABLE afterwards (webhook-enforced, like owner): moving
	// workloads across namespaces is a recreate, never a mutation. Empty
	// = the CR's own namespace (legacy/platform placement).
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// WorkloadName names the Deployment/Service ("<name>") and home PVC
	// ("<name>-home"). Derived from the display name at creation
	// (sanitized, collision-suffixed) and IMMUTABLE: renaming the display
	// name later never renames compute. Empty = legacy "ws-<CR name>".
	// +optional
	WorkloadName string `json:"workloadName,omitempty"`

	// HomeVolumeName adopts an EXISTING PVC as this workspace's home
	// instead of creating "<workloadName>-home": the reuse path for a
	// volume retained from a deleted workspace. IMMUTABLE, and vetted by
	// the webhook: the PVC must live in the target namespace, belong to
	// the same owner (LabelOwner) and not be another workspace's live
	// home. Empty = a fresh volume.
	// +optional
	HomeVolumeName string `json:"homeVolumeName,omitempty"`

	// Resources overrides the template resources for this workspace only.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Overrides are creator-supplied deviations from the template pod
	// spec. The admission webhook rejects any field the template's
	// override policy does not allow for this creator.
	// +optional
	Overrides *WorkspaceOverrides `json:"overrides,omitempty"`
}

// WorkspaceOverrides mirrors the template's workload passthrough for the
// fields a creator is allowed to change or extend.
type WorkspaceOverrides struct {
	// Env entries are merged over the template's env (same name wins).
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// SecurityContext replaces the template container security context.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// PodSecurityContext replaces the template pod security context.
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// Volumes are appended to the template volumes (same name wins).
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts are appended to the template mounts (same name wins).
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// NodeSelector entries are merged over the template's.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations are appended to the template's.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Protocol picks this workspace's default protocol among the
	// template's declared protocols.
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Schedule replaces the template's uptime/downtime schedule for this
	// workspace (allowed only when the template delegates "schedule").
	// +optional
	Schedule *WorkspaceSchedule `json:"schedule,omitempty"`

	// Labels/Annotations are merged under the template's workload
	// metadata (allowed only when the template delegates "metadata";
	// reserved keys rejected, operator labels always win).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// WorkspaceStatus is the observed state, written exclusively via the status
// subresource.
type WorkspaceStatus struct {
	// Phase is the traffic-light state for the fleet dashboard.
	// +optional
	Phase WorkspacePhase `json:"phase,omitempty"`

	// ObservedGeneration is the spec generation last acted upon.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// OS is resolved from the template at provisioning time.
	// +optional
	OS OSType `json:"os,omitempty"`

	// Address is the in-cluster DNS name guacd connects to.
	// +optional
	Address string `json:"address,omitempty"`

	// Port is the desktop port (VNC/RDP) on Address.
	// +optional
	Port int32 `json:"port,omitempty"`

	// Protocol is the default guacamole protocol for this workspace.
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Protocols are all protocols the workspace serves, resolved from the
	// template at provisioning time.
	// +optional
	Protocols []WorkspaceProtocolStatus `json:"protocols,omitempty"`

	// PVCName is the persistent home volume backing this workspace.
	// +optional
	PVCName string `json:"pvcName,omitempty"`

	// NextTransition is the next scheduled up/down transition, when the
	// workspace has a schedule. Shown on the portal card and detail view.
	// +optional
	NextTransition *ScheduledTransition `json:"nextTransition,omitempty"`

	// Conditions follow the standard Kubernetes condition conventions.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ScheduledTransition is the next planned lifecycle change.
type ScheduledTransition struct {
	// Time is when the transition fires.
	Time metav1.Time `json:"time"`
	// Up is true for a start (uptime) edge, false for a stop (downtime) edge.
	Up bool `json:"up"`
}

// WorkspaceProtocolStatus is one protocol endpoint of a running workspace.
type WorkspaceProtocolStatus struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
	// Default marks the protocol used when the user picks none.
	// +optional
	Default bool `json:"default,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ws
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Drifted",type=string,JSONPath=`.status.conditions[?(@.type=="TemplateDrifted")].status`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateRef`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Workspace is a user's remote desktop, declared as a Kubernetes resource.
type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkspaceSpec `json:"spec"`
	// +optional
	Status WorkspaceStatus `json:"status,omitempty"`
}

// EffectiveTargetNamespace is where this workspace's workloads live: the
// frozen spec.targetNamespace, or the CR's namespace (legacy placement).
func (w *Workspace) EffectiveTargetNamespace() string {
	if w.Spec.TargetNamespace != "" {
		return w.Spec.TargetNamespace
	}
	return w.Namespace
}

// EffectiveWorkloadName names this workspace's Deployment/Service (the
// home PVC appends "-home"): the frozen spec.workloadName, or the legacy
// "ws-<CR name>".
func (w *Workspace) EffectiveWorkloadName() string {
	if w.Spec.WorkloadName != "" {
		return w.Spec.WorkloadName
	}
	return "ws-" + w.Name
}

// +kubebuilder:object:root=true

// WorkspaceList contains a list of Workspace.
type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workspace{}, &WorkspaceList{})
}
