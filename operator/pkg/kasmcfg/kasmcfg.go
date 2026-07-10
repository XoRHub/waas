// Package kasmcfg centralizes the kasmvnc.yaml keys WaaS owns, so the
// controller (which stamps them), the admission webhook and the api-server
// (which both reject an admin from setting them) can never drift apart.
package kasmcfg

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// ManagedClipboardKeyPaths are the kasmvnc.yaml keys WaaS derives from
// WorkspacePolicy.Clipboard and stamps over the admin's kasmvncConfig at
// reconcile. Order matters: the controller pairs these paths with
// [copyAllowed, pasteAllowed, false] (clipboard copy, clipboard paste,
// client-override-off) when applying them. An admin who sets any of these
// in a template would be silently overwritten, so both the webhook and
// the api-server refuse them.
var ManagedClipboardKeyPaths = [][]string{
	{"data_loss_prevention", "clipboard", "server_to_client", "enabled"},
	{"data_loss_prevention", "clipboard", "client_to_server", "enabled"},
	{"runtime_configuration", "allow_client_to_override_kasm_server_settings"},
}

// PolicyManagedClipboardKeys returns the dot-joined managed keys that the
// raw kasmvnc.yaml sets, or an error if it is not valid YAML.
func PolicyManagedClipboardKeys(raw string) ([]string, error) {
	root := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	var found []string
	for _, path := range ManagedClipboardKeyPaths {
		if hasNestedKey(root, path...) {
			found = append(found, strings.Join(path, "."))
		}
	}
	return found, nil
}

// hasNestedKey reports whether the nested key path is present in the map
// (any leaf value, including false/null).
func hasNestedKey(root map[string]any, keys ...string) bool {
	cur := root
	for i, k := range keys {
		v, ok := cur[k]
		if !ok {
			return false
		}
		if i == len(keys)-1 {
			return true
		}
		cur, ok = v.(map[string]any)
		if !ok {
			return false
		}
	}
	return false
}
