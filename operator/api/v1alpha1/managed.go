package v1alpha1

import "k8s.io/apimachinery/pkg/runtime/schema"

// LabelCleanup, stamped on operator-created namespaces, freezes the
// template's namespace cleanup policy at creation time (values: the
// NamespaceCleanupPolicy strings, "Retain" / "DeleteWhenEmpty"). The
// namespace janitor reads THIS label, never the template: a template
// deleted before its workspaces must not silently turn DeleteWhenEmpty
// into Retain.
const LabelCleanup = "waas.xorhub.io/cleanup"

// VirtualMachineGVK identifies kubevirt.io/v1 VirtualMachine (KubeVirt is
// an optional runtime dependency, managed unstructured).
var VirtualMachineGVK = schema.GroupVersionKind{Group: "kubevirt.io", Version: "v1", Kind: "VirtualMachine"}

// The lists below are the SINGLE inventory of what the operator creates
// for a workspace. The teardown finalizer, the namespace janitor's
// emptiness check, the zero-orphan e2e sweep and the orphan audit all
// derive from here: adding a new resource type to the reconciler without
// registering it makes the e2e assertion fail.

// WorkspaceContentGVKs are the per-workspace objects (stamped with
// LabelWorkspace). Any of these present in a namespace marks it as
// non-empty for the DeleteWhenEmpty policy.
func WorkspaceContentGVKs() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		{Group: "", Version: "v1", Kind: "Pod"},
		{Group: "", Version: "v1", Kind: "Service"},
		{Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		VirtualMachineGVK,
	}
}

// NamespaceBootstrapGVKs are the per-namespace accompanying objects
// (quota, default ingress policy). They belong to the namespace's
// lifecycle — deleted by its cascade, never individually — and do not
// count as content for DeleteWhenEmpty.
func NamespaceBootstrapGVKs() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		{Group: "", Version: "v1", Kind: "ResourceQuota"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
	}
}

// ManagedNamespacedGVKs is every namespaced type the operator creates:
// the audit surface for orphan detection.
func ManagedNamespacedGVKs() []schema.GroupVersionKind {
	return append(WorkspaceContentGVKs(), NamespaceBootstrapGVKs()...)
}
