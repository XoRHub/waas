// Package kasmcfg centralizes the kasmvnc.yaml keys and the container env
// WaaS owns, so the controller (which stamps them), the admission webhook
// and the api-server (which both reject an admin from setting them) can
// never drift apart.
package kasmcfg

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/xorhub/waas/shared/kasmvncsidecar"
)

// The policy-derived kasmvnc.yaml keys, stamped by the controller over the
// admin's kasmvncConfig at reconcile.
var (
	// Clipboard DLP, from WorkspacePolicy.Clipboard.
	KeyClipboardCopy  = []string{"data_loss_prevention", "clipboard", "server_to_client", "enabled"}
	KeyClipboardPaste = []string{"data_loss_prevention", "clipboard", "client_to_server", "enabled"}
	// Always forced off: with it on, the KasmVNC client can re-enable at
	// connect time what the server-side DLP closed.
	KeyClientOverride = []string{"runtime_configuration", "allow_client_to_override_kasm_server_settings"}
	// Watermark text, from WorkspacePolicy.Watermark. The image variant
	// is owned too but never stamped, only refused in templates: vncserver
	// exits when both are set, so a template image would turn a policy
	// watermark into a crash loop.
	KeyWatermarkText  = []string{"data_loss_prevention", "watermark", "text", "template"}
	KeyWatermarkImage = []string{"data_loss_prevention", "watermark", "image"}
)

// ManagedKeyPaths are the kasmvnc.yaml keys WaaS owns. An admin who sets
// any of these in a template would be silently overwritten (or crash the
// session, for the watermark image), so both the webhook and the
// api-server refuse them, and the template editor shows them locked.
var ManagedKeyPaths = [][]string{KeyClipboardCopy, KeyClipboardPaste, KeyClientOverride, KeyWatermarkText, KeyWatermarkImage}

// PolicyManagedKeys returns the dot-joined managed keys that the raw
// kasmvnc.yaml sets, or an error if it is not valid YAML.
func PolicyManagedKeys(raw string) ([]string, error) {
	root := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	var found []string
	for _, path := range ManagedKeyPaths {
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

// WatermarkMaxLength and watermarkPattern are the WorkspacePolicy.Watermark
// CRD markers (MaxLength, Pattern), mirrored here for the api-server's
// clean 400 and the controller's defense in depth; TestWatermarkMarkersMatchCRD
// keeps them in lockstep. KasmVNC's vncserver splices ${NAME} from its
// environment into the text, then hands it to Xvnc inside single quotes
// WITHOUT escaping, through a shell: a ' breaks out of the argument, and
// so does a $ (an env value set via the template or override env can
// carry a quote — and ${VNC_PW} would draw the password on screen).
// Control characters are refused too: a watermark is one line.
const WatermarkMaxLength = 200

var watermarkPattern = regexp.MustCompile(`^[^'$\x00-\x1f\x7f]*$`)

// ValidateWatermark reports why an admin watermark template is refused.
func ValidateWatermark(text string) error {
	if utf8.RuneCountInString(text) > WatermarkMaxLength {
		return fmt.Errorf("at most %d characters", WatermarkMaxLength)
	}
	if !watermarkPattern.MatchString(text) {
		return fmt.Errorf("single quotes, $ and control characters are not allowed")
	}
	return nil
}

// unsafeWatermarkRune is everything a username may not contribute to the
// watermark text (see WatermarkText).
var unsafeWatermarkRune = regexp.MustCompile(`[^A-Za-z0-9._@-]`)

// WatermarkText renders a policy watermark template for one workspace:
// {user} becomes the owner's username. The admin's template is held to
// ValidateWatermark (an invalid one — admission bypassed — renders no
// watermark rather than a shell break-out); the username, the one input
// an admin does not type, is reduced to [A-Za-z0-9._@-] (every other rune
// becomes _): no quote can leave the shell argument, no $ or % can become
// an env or strftime expansion. The YAML side needs no care: the value
// is marshalled, never templated.
func WatermarkText(template, username string) string {
	if template == "" || ValidateWatermark(template) != nil {
		return ""
	}
	return strings.ReplaceAll(template, "{user}", unsafeWatermarkRune.ReplaceAllString(username, "_"))
}

// PrinterEnv is the one side-service toggle a policy can turn on:
// printing is session→browser file egress, so it follows
// WorkspacePolicy.FileTransfer.Download.
const PrinterEnv = "KASM_SVC_PRINTER"

// SideServiceEnv are the kasmweb startup toggles of the image's
// closed-source side services (audio out/in, uploads, gamepad, webcam,
// printer, smartcard — vnc_startup.sh starts each unless set to 0).
// WaaS proxies none of them, so the controller stamps every one on
// kasmvnc containers: "0" as default-off hardening (fewer listeners,
// fewer closed-source binaries running), PrinterEnv "1" when the owner's
// policy allows download. This is NOT enforcement against the session
// user — vnc_startup.sh sources the user's persistent $HOME/.bashrc
// before starting Xvnc, and the user owns Xvnc itself, so `export
// KASM_SVC_PRINTER=1` + restarting Xvnc undoes it from inside the
// session. Nothing is a hard file-egress control against that occupant
// (print jobs ride the RFB stream, not /Downloads; see docs/kasmvnc.md):
// the wwt path blocks (isDownloadsPath) only close the HTTP routes. Like
// the managed keys, a template
// value here would be silently overwritten, so the webhook and the
// api-server refuse them.
var SideServiceEnv = []string{
	"KASM_SVC_AUDIO",
	"KASM_SVC_AUDIO_INPUT",
	"KASM_SVC_UPLOADS",
	"KASM_SVC_GAMEPAD",
	"KASM_SVC_WEBCAM",
	PrinterEnv,
	"KASM_SVC_SMARTCARD",
}

// ManagedEnv is every env name the controller stamps on kasmvnc
// containers: the side-service toggles above, plus PULSE_SERVER, which
// points the desktop's audio clients at the kasmvnc-sidecar's
// PulseAudio (the image's own daemon never starts: KASM_SVC_AUDIO=0
// skips it). Same soft-control caveat as the toggles.
var ManagedEnv = append(slices.Clone(SideServiceEnv), kasmvncsidecar.EnvPulseServer)

// ManagedEnvNames returns the names in env that WaaS owns on kasmvnc
// containers (ManagedEnv).
func ManagedEnvNames(env []corev1.EnvVar) []string {
	var found []string
	for _, e := range env {
		if slices.Contains(ManagedEnv, e.Name) {
			found = append(found, e.Name)
		}
	}
	return found
}
