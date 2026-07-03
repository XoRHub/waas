package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OSType is the operating-system family of a workspace.
// +kubebuilder:validation:Enum=linux;windows
type OSType string

const (
	OSLinux   OSType = "linux"
	OSWindows OSType = "windows"
)

// WorkspaceTemplateSpec defines the desired shape of workspaces stamped from
// this template. Templates are cluster-side configuration ("workspaces as
// code"): admins manage them via kubectl/GitOps or through the API server,
// which itself only manipulates this CRD.
type WorkspaceTemplateSpec struct {
	// DisplayName is the human-facing name shown in the portal.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Description is shown to admins when picking a template.
	// +optional
	Description string `json:"description,omitempty"`

	// OS selects the workspace kind: linux (pod + VNC) or windows
	// (KubeVirt VM + RDP). Windows requires KubeVirt in the cluster and is
	// rejected by the validating webhook otherwise.
	OS OSType `json:"os"`

	// Image is the container image (linux) or containerdisk image (windows)
	// providing the desktop environment.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Port is the port the in-workspace desktop server listens on.
	// Defaults to 5901 (VNC) for linux and 3389 (RDP) for windows.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Resources are the compute resources of the workspace pod/VM.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// HomeSize is the size of the persistent home volume. The home PVC is
	// decoupled from the pod lifecycle: destroying a workspace keeps it.
	// +optional
	HomeSize *resource.Quantity `json:"homeSize,omitempty"`

	// StorageClassName selects the storage class for home volumes.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Env is extra environment injected into linux workspace pods.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// DesktopPort returns the effective desktop port for this template.
func (s *WorkspaceTemplateSpec) DesktopPort() int32 {
	if s.Port != 0 {
		return s.Port
	}
	if s.OS == OSWindows {
		return 3389
	}
	return 5901
}

// Protocol returns the guacamole protocol matching the template OS.
func (s *WorkspaceTemplateSpec) Protocol() string {
	if s.OS == OSWindows {
		return "rdp"
	}
	return "vnc"
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=wst
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="OS",type=string,JSONPath=`.spec.os`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceTemplate is the blueprint a Workspace references.
type WorkspaceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkspaceTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// WorkspaceTemplateList contains a list of WorkspaceTemplate.
type WorkspaceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspaceTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkspaceTemplate{}, &WorkspaceTemplateList{})
}
