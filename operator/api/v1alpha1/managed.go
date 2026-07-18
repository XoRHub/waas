package v1alpha1

import "k8s.io/apimachinery/pkg/runtime/schema"

// LabelCleanup, stamped on operator-created namespaces, freezes the
// template's namespace cleanup policy at creation time (values: the
// NamespaceCleanupPolicy strings, "Retain" / "DeleteWhenEmpty"). The
// namespace janitor reads THIS label, never the template: a template
// deleted before its workspaces must not silently turn DeleteWhenEmpty
// into Retain.
const LabelCleanup = "waas.xorhub.io/cleanup"

// LabelPullSecret marks the operator's per-namespace copies of registry
// pull credentials (WorkspaceImage.spec.imagePullSecretRef). They are
// shared by every workspace of the namespace: NOT workspace content
// (the janitor's emptiness check skips them — they must never pin a
// DeleteWhenEmpty namespace), reclaimed by the namespace cascade.
const LabelPullSecret = "waas.xorhub.io/pull-secret"

// AnnotationTemplateMeta records, on the home PVC, which label and
// annotation keys were stamped from the template's homeVolume block —
// the removal ledger: a key present here but gone from the template is
// removed at reconcile; keys an admin set by hand are never listed and
// therefore never touched. Value: compact JSON
// {"labels":[...],"annotations":[...]}, keys sorted.
const AnnotationTemplateMeta = "waas.xorhub.io/template-meta"

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
		// Generated credentials pod-namespace copies: the kasm/desktop
		// password Secret is named like the workload (the name-based
		// teardown sweep catches it); the generated-ssh public-key copy
		// is "<workload>-ssh" and teardownPlacement deletes it explicitly.
		{Group: "", Version: "v1", Kind: "Secret"},
		// User-level KasmVNC config, same naming convention.
		{Group: "", Version: "v1", Kind: "ConfigMap"},
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
