// Package metakeys is the server-side denylist for user-supplied labels
// and annotations (template placement/workload metadata and workspace
// overrides). A label key can change platform behavior — operator
// selectors, sidecar injection, Pod Security admission, GitOps ownership —
// so the deny decision must live server-side (webhook + reconciler
// re-check), never in the UI.
package metakeys

import (
	"fmt"
	"strings"
)

// deniedDomains rejects a key when its prefix domain equals or is a
// subdomain of one of these. Covers kubernetes.io/ and every
// *.kubernetes.io/ (kubectl, pod-security, node restrictions...),
// the platform's own domain, GitOps ownership, and the common mutating
// injectors (service meshes, Vault agent).
var deniedDomains = []string{
	"kubernetes.io",
	"k8s.io",
	"xorhub.io", // waas.xorhub.io included: platform labels are never user-writable
	"argoproj.io",
	"istio.io",
	"linkerd.io",
	"vault.hashicorp.com",
	"cilium.io",
	"openshift.io",
}

// deniedBare rejects un-domained keys that tooling treats as special.
var deniedBare = map[string]bool{
	"name": false, // allowed — kept here as documentation of the decision
}

// CheckKey returns an error when key is reserved. Keys without a "/" are
// allowed unless explicitly listed: plain names cannot collide with
// platform machinery.
func CheckKey(key string) error {
	if key == "" {
		return fmt.Errorf("empty metadata key")
	}
	domain, _, found := strings.Cut(key, "/")
	if !found {
		if deniedBare[key] {
			return fmt.Errorf("metadata key %q is reserved", key)
		}
		return nil
	}
	for _, denied := range deniedDomains {
		if domain == denied || strings.HasSuffix(domain, "."+denied) {
			return fmt.Errorf("metadata key %q is reserved (domain %q is platform- or Kubernetes-owned)", key, domain)
		}
	}
	return nil
}

// Check validates every key of a user-supplied label/annotation map.
func Check(meta map[string]string) error {
	for key := range meta {
		if err := CheckKey(key); err != nil {
			return err
		}
	}
	return nil
}

// MergeAllowed lays user metadata under platform metadata: platform keys
// always win, whatever the denylist missed.
func MergeAllowed(user, platform map[string]string) map[string]string {
	if len(user) == 0 && len(platform) == 0 {
		return nil
	}
	out := make(map[string]string, len(user)+len(platform))
	for k, v := range user {
		if CheckKey(k) == nil {
			out[k] = v
		}
	}
	for k, v := range platform {
		out[k] = v
	}
	return out
}
