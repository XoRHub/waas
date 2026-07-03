// Package kubevirt detects whether KubeVirt is installed and provides the
// unstructured GVKs the operator uses to manage Windows VMs. KubeVirt is an
// optional runtime dependency: it is auto-detected at startup, never a Helm
// prerequisite, and Windows workspaces are rejected loudly when it is absent.
package kubevirt

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// Group is the KubeVirt API group probed at startup.
const Group = "kubevirt.io"

// VirtualMachineGVK identifies kubevirt.io/v1 VirtualMachine.
var VirtualMachineGVK = schema.GroupVersionKind{Group: Group, Version: "v1", Kind: "VirtualMachine"}

// Detect reports whether the KubeVirt API group is served by the cluster.
func Detect(cfg *rest.Config) (bool, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, fmt.Errorf("building discovery client: %w", err)
	}
	groups, err := dc.ServerGroups()
	if err != nil {
		return false, fmt.Errorf("listing API groups: %w", err)
	}
	for _, g := range groups.Groups {
		if g.Name == Group {
			return true, nil
		}
	}
	return false, nil
}
