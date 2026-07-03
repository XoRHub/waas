package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspacePhase is the coarse lifecycle state surfaced to the fleet dashboard.
// +kubebuilder:validation:Enum=Pending;Provisioning;Running;Stopped;Failed;Terminating
type WorkspacePhase string

const (
	PhasePending      WorkspacePhase = "Pending"
	PhaseProvisioning WorkspacePhase = "Provisioning"
	PhaseRunning      WorkspacePhase = "Running"
	PhaseStopped      WorkspacePhase = "Stopped"
	PhaseFailed       WorkspacePhase = "Failed"
	PhaseTerminating  WorkspacePhase = "Terminating"
)

// Condition types set on Workspace.
const (
	ConditionReady = "Ready"
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

	// Resources overrides the template resources for this workspace only.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
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

	// Protocol is the guacamole protocol ("vnc" or "rdp").
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// PVCName is the persistent home volume backing this workspace.
	// +optional
	PVCName string `json:"pvcName,omitempty"`

	// Conditions follow the standard Kubernetes condition conventions.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ws
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
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
