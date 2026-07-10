// Package params is the single registry of guacd connection parameters the
// platform knows, per protocol. One vocabulary end to end: the CR key IS
// the guacd wire name.
//
// Every consumer derives from this table, so they can never disagree:
//   - the admission webhook validates template/workspace params (values
//     AND exposure tier) server-side;
//   - the api-server validates connect-time overrides and serves the
//     registry to the frontend (GET /api/v1/meta/protocols), which renders
//     its forms from it;
//   - the docs mapping (docs/guacd-parameters.md) is generated from it.
//
// Adding a parameter = adding one entry here (see the guide in
// docs/guacd-parameters.md).
package params

import (
	"fmt"
	"sort"
	"strconv"
)

// Tier classifies who may see and set a parameter.
type Tier string

const (
	// TierUI: exposed in portal forms (template editor, workspace
	// creation, connection settings, in-session overlay).
	TierUI Tier = "ui"
	// TierAdvanced: rendered behind the advanced disclosure of its
	// section in every param form — template editor and end-user
	// connect-time forms alike. The tier only drives placement:
	// validation policy is identical to TierUI (only TierPlatform is
	// banned), and a template still delegates each name explicitly via
	// userParams/expertUserParams before users may override it.
	TierAdvanced Tier = "advanced"
	// TierPlatform: owned by the platform. Never accepted in a CR,
	// template or connect override, whoever asks: either the platform
	// forces the value itself (hostname, port) or the parameter is a
	// security/topology hazard (gateways, repeaters, raw credentials).
	TierPlatform Tier = "platform"
)

// Category groups parameters into the thematic sections of the param
// forms (display, audio, clipboard, …). Purely presentational: it drives
// section headings and ordering, never validation. Platform-owned
// parameters carry one too so the registry coherence test stays trivial,
// even though they are never rendered in a form.
type Category string

const (
	CategoryDisplay    Category = "display"
	CategoryAudio      Category = "audio"
	CategoryInput      Category = "input"
	CategoryClipboard  Category = "clipboard"
	CategorySession    Category = "session"
	CategorySecurity   Category = "security"
	CategoryConnection Category = "connection"
)

// AllCategories returns every category in canonical display order.
// ForProtocol sorts by it, so the frontend renders sections in this
// order straight from the payload without carrying its own copy.
func AllCategories() []Category {
	return []Category{
		CategoryDisplay, CategoryAudio, CategoryInput, CategoryClipboard,
		CategorySession, CategorySecurity, CategoryConnection,
	}
}

// Kind is the value type, driving both validation and form widgets.
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindEnum   Kind = "enum"
)

// Param describes one guacd connection parameter.
type Param struct {
	// Name is the guacd wire name and the CR map key (single vocabulary).
	Name string `json:"name"`
	// Protocols this parameter applies to ("vnc", "rdp", "ssh").
	Protocols []string `json:"protocols"`
	Kind      Kind     `json:"kind"`
	// Enum values when Kind == KindEnum.
	Enum []string `json:"enum,omitempty"`
	// Min/Max bound KindInt values (inclusive) when non-nil.
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
	// Default documents guacd's own default (display only, never sent).
	Default string `json:"default,omitempty"`
	Tier    Tier   `json:"tier"`
	// Category is the thematic form section the parameter renders under.
	Category Category `json:"category"`
	// Live: the effect can be applied mid-session by the client or the
	// wwt proxy without reconnecting. Everything else needs a reconnect
	// (guacd fixes parameters at connect time) and the UI must say so.
	Live        bool   `json:"live"`
	Description string `json:"description"`
}

func intp(v int) *int { return &v }

// registry is THE table. Keep it sorted by protocol concern, not
// alphabetically — reviews read it like documentation.
var registry = []Param{
	// ------------------------------------------------------------- shared
	{
		Name: "read-only", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategorySecurity,
		Description: "View-only session: display without mouse/keyboard input.",
	},
	{
		Name: "disable-copy", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryClipboard, Live: true,
		Description: "Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable.",
	},
	{
		Name: "disable-paste", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryClipboard, Live: true,
		Description: "Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable.",
	},

	// ---------------------------------------------------------------- vnc
	{
		Name: "color-depth", Protocols: []string{"vnc", "rdp"}, Kind: KindEnum,
		Enum: []string{"8", "16", "24", "32"}, Default: "24", Tier: TierUI, Category: CategoryDisplay,
		Description: "Display color depth in bits per pixel; lower saves bandwidth.",
	},
	{
		Name: "swap-red-blue", Protocols: []string{"vnc"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryDisplay,
		Description: "Fix red/blue channel inversion produced by some VNC servers.",
	},
	{
		Name: "cursor", Protocols: []string{"vnc"}, Kind: KindEnum,
		Enum: []string{"local", "remote"}, Default: "local", Tier: TierUI, Category: CategoryDisplay,
		Description: "Render the mouse cursor locally (responsive) or remotely (accurate for cursor-morphing apps).",
	},
	{
		Name: "force-lossless", Protocols: []string{"vnc"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryDisplay,
		Description: "Force lossless compression (sharp text, higher bandwidth).",
	},
	{
		Name: "enable-audio", Protocols: []string{"vnc"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryAudio,
		Description: "Stream audio from the workspace's PulseAudio server (requires the image to run one).",
	},
	{
		Name: "audio-servername", Protocols: []string{"vnc"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategoryAudio,
		Description: "PulseAudio server name when it differs from the VNC hostname.",
	},
	{
		Name: "encodings", Protocols: []string{"vnc"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Space-separated VNC encodings offered to the server (expert tuning).",
	},
	{
		Name: "autoretry", Protocols: []string{"vnc"}, Kind: KindInt,
		Min: intp(0), Max: intp(10), Tier: TierAdvanced, Category: CategoryConnection,
		Description: "Connection retries before giving up (covers desktops still booting).",
	},
	{
		Name: "clipboard-encoding", Protocols: []string{"vnc"}, Kind: KindEnum,
		Enum: []string{"ISO8859-1", "UTF-8", "UTF-16", "CP1252"}, Default: "ISO8859-1", Tier: TierAdvanced, Category: CategoryClipboard,
		Description: "Character encoding the VNC server uses for clipboard data.",
	},

	// ---------------------------------------------------------------- rdp
	{
		// Kept for remote RDP workspaces, where guacd talks to a real
		// external RDP server and this parameter drives guacd's native
		// resize negotiation. In-cluster desktops never use it: WaaS
		// resizes them by exec'ing waas-resize in the pod, a path that
		// bypasses guacd entirely (docs/session-resize.md).
		Name: "resize-method", Protocols: []string{"rdp"}, Kind: KindEnum,
		Enum: []string{"display-update", "reconnect"}, Default: "display-update", Tier: TierUI, Category: CategoryDisplay,
		Description: "How guacd propagates resizes to a remote RDP server (display-update = live resize). No effect on in-cluster desktops, which WaaS resizes via pod exec.",
	},
	{
		Name: "security", Protocols: []string{"rdp"}, Kind: KindEnum,
		Enum: []string{"any", "nla", "tls", "rdp"}, Default: "any", Tier: TierAdvanced, Category: CategorySecurity,
		Description: "RDP security negotiation mode; in-cluster Windows VMs may need a specific one.",
	},
	{
		Name: "ignore-cert", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "true", Tier: TierAdvanced, Category: CategorySecurity,
		Description: "Accept the RDP server certificate unverified. Acceptable in-cluster (self-signed VM certs); the connection never leaves the cluster network.",
	},
	{
		Name: "disable-audio", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierUI, Category: CategoryAudio,
		Description: "Disable audio redirection from the remote desktop.",
	},
	{
		Name: "enable-audio-input", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryAudio,
		Description: "Redirect the local microphone into the remote session.",
	},
	{
		// TierUI (was advanced, free string): keyboard layout is a "simple
		// mode" parameter; the enum is the list guacd 1.5 actually ships.
		Name: "server-layout", Protocols: []string{"rdp"}, Kind: KindEnum,
		Enum: []string{
			"en-us-qwerty", "en-gb-qwerty", "cs-cz-qwertz", "da-dk-qwerty",
			"de-ch-qwertz", "de-de-qwertz", "es-es-qwerty", "es-latam-qwerty",
			"fr-be-azerty", "fr-ca-qwerty", "fr-ch-qwertz", "fr-fr-azerty",
			"hu-hu-qwertz", "it-it-qwerty", "ja-jp-qwerty", "nl-nl-qwerty",
			"no-no-qwerty", "pl-pl-qwertz", "pt-br-qwerty", "pt-pt-qwerty",
			"ro-ro-qwerty", "sv-se-qwerty", "tr-tr-qwerty", "failsafe",
		},
		Default: "en-us-qwerty", Tier: TierUI, Category: CategoryInput,
		Description: "Keyboard layout the RDP server expects. Left unset, the platform auto-detects it from the browser locale (failsafe sends Unicode events).",
	},
	{
		Name: "console", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategorySession,
		Description: "Attach to the console (admin) session instead of a new one.",
	},
	{
		Name: "enable-wallpaper", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Render the desktop wallpaper (bandwidth for cosmetics).",
	},
	{
		Name: "enable-font-smoothing", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Enable ClearType font smoothing.",
	},
	{
		Name: "enable-desktop-composition", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Enable Windows Aero desktop composition effects.",
	},
	{
		Name: "enable-full-window-drag", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Render window contents while dragging (bandwidth for comfort).",
	},
	{
		Name: "enable-menu-animations", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Enable menu open/close animations.",
	},
	{
		Name: "enable-theming", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Enable desktop/window theming.",
	},
	{
		Name: "disable-bitmap-caching", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Disable the RDP bitmap cache (workaround for buggy servers).",
	},
	{
		Name: "disable-offscreen-caching", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Disable caching of off-screen regions (workaround for buggy servers).",
	},
	{
		Name: "disable-glyph-caching", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Disable glyph caching (workaround for text rendering glitches).",
	},
	{
		Name: "normalize-clipboard", Protocols: []string{"rdp"}, Kind: KindEnum,
		Enum: []string{"preserve", "text"}, Default: "preserve", Tier: TierAdvanced, Category: CategoryClipboard,
		Description: "Line-ending normalization applied to clipboard text.",
	},
	{
		Name: "initial-program", Protocols: []string{"rdp"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySession,
		Description: "Program launched instead of the full desktop (kiosk-style templates).",
	},
	{
		Name: "client-name", Protocols: []string{"rdp"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySession,
		Description: "Client hostname announced to the RDP server (some session brokers key on it).",
	},
	{
		Name: "console-audio", Protocols: []string{"rdp"}, Kind: KindBool,
		Default: "false", Tier: TierAdvanced, Category: CategoryAudio,
		Description: "Play audio on the server console instead of streaming it to the client.",
	},
	{
		Name: "timezone", Protocols: []string{"rdp", "ssh"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySession,
		Description: "IANA timezone forwarded to the session (e.g. Europe/Paris).",
	},

	// ---------------------------------------------------------------- ssh
	{
		Name: "font-size", Protocols: []string{"ssh"}, Kind: KindInt,
		Min: intp(6), Max: intp(48), Default: "12", Tier: TierUI, Category: CategoryDisplay,
		Description: "Terminal font size in points.",
	},
	{
		Name: "color-scheme", Protocols: []string{"ssh"}, Kind: KindEnum,
		Enum:    []string{"black-white", "gray-black", "green-black", "white-black"},
		Default: "gray-black", Tier: TierUI, Category: CategoryDisplay,
		Description: "Terminal color scheme.",
	},
	{
		Name: "font-name", Protocols: []string{"ssh"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Terminal font family (must exist server-side in guacd).",
	},
	{
		Name: "scrollback", Protocols: []string{"ssh"}, Kind: KindInt,
		Min: intp(0), Max: intp(100000), Default: "1000", Tier: TierAdvanced, Category: CategoryDisplay,
		Description: "Scrollback buffer size in rows.",
	},
	{
		Name: "terminal-type", Protocols: []string{"ssh"}, Kind: KindString,
		Default: "linux", Tier: TierAdvanced, Category: CategorySession,
		Description: "TERM value announced to the SSH server.",
	},
	{
		Name: "command", Protocols: []string{"ssh"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySession,
		Description: "Command to run instead of an interactive shell (kiosk-style templates).",
	},
	{
		Name: "server-alive-interval", Protocols: []string{"ssh"}, Kind: KindInt,
		Min: intp(0), Max: intp(3600), Tier: TierAdvanced, Category: CategoryConnection,
		Description: "SSH keep-alive interval in seconds.",
	},
	{
		Name: "backspace", Protocols: []string{"ssh"}, Kind: KindInt,
		Min: intp(1), Max: intp(255), Default: "127", Tier: TierAdvanced, Category: CategoryInput,
		Description: "Code sent by the backspace key (127 = ASCII DEL, 8 = BS for legacy hosts).",
	},
	{
		Name: "locale", Protocols: []string{"ssh"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySession,
		Description: "LANG value forwarded to the SSH session (server must accept env forwarding).",
	},
	{
		Name: "host-key", Protocols: []string{"ssh"}, Kind: KindString,
		Tier: TierAdvanced, Category: CategorySecurity,
		Description: "Expected server host key (Base64); connection is refused on mismatch.",
	},

	// ----------------------------------------------------- platform-owned
	// Listed so the docs generator and the webhook agree on WHY they are
	// rejected; the platform either injects them itself or bans them.
	{
		Name: "hostname", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "Always the workspace service address, resolved by the operator.",
	},
	{
		Name: "port", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindInt, Tier: TierPlatform, Category: CategoryConnection,
		Description: "Always the workspace protocol port, resolved by the operator.",
	},
	{
		Name: "username", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "Desktop credential — comes from the protocol's credentials Secret, never from a CR param.",
	},
	{
		Name: "password", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "Desktop credential — comes from the protocol's credentials Secret, never from a CR param.",
	},
	{
		Name: "private-key", Protocols: []string{"ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "SSH private key — comes from the protocol's credentials Secret, never from a CR param.",
	},
	{
		Name: "passphrase", Protocols: []string{"ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "SSH key passphrase — comes from the protocol's credentials Secret, never from a CR param.",
	},
	{
		Name: "dest-host", Protocols: []string{"vnc"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "VNC repeater redirection — banned: would let a CR reroute guacd to an arbitrary host.",
	},
	{
		Name: "dest-port", Protocols: []string{"vnc"}, Kind: KindInt, Tier: TierPlatform, Category: CategoryConnection,
		Description: "VNC repeater redirection — banned (see dest-host).",
	},
	{
		Name: "gateway-hostname", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP gateway — banned: workspace traffic never leaves the cluster network.",
	},
	{
		Name: "gateway-port", Protocols: []string{"rdp"}, Kind: KindInt, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP gateway — banned (see gateway-hostname).",
	},
	{
		Name: "gateway-username", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP gateway credential — banned (see gateway-hostname).",
	},
	{
		Name: "gateway-password", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP gateway credential — banned (see gateway-hostname).",
	},
	{
		Name: "enable-sftp", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool, Tier: TierPlatform, Category: CategorySession,
		Description: "File transfer — platform-owned until the file-transfer feature ships with its own policy gate.",
	},
	{
		Name: "enable-drive", Protocols: []string{"rdp"}, Kind: KindBool, Tier: TierPlatform, Category: CategorySession,
		Description: "Drive redirection — platform-owned until the file-transfer feature ships with its own policy gate.",
	},
	{
		Name: "wol-send-packet", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool, Tier: TierPlatform, Category: CategoryConnection,
		Description: "Wake-on-LAN — meaningless in-cluster, banned.",
	},
	{
		Name: "domain", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP credential — comes from the protocol's credentials Secret, never from a CR param.",
	},
	{
		Name: "disable-auth", Protocols: []string{"rdp"}, Kind: KindBool, Tier: TierPlatform, Category: CategorySecurity,
		Description: "Disables RDP authentication entirely — banned: authentication is platform policy (see RDP_AUTH_ENABLED image contract).",
	},
	{
		Name: "static-channels", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategorySession,
		Description: "Raw static virtual channel pass-through — banned: uncontrolled side channel.",
	},
	{
		Name: "load-balance-info", Protocols: []string{"rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "RDP broker routing token — platform topology concern, banned in CRs.",
	},
	{
		Name: "recording-path", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategorySession,
		Description: "Session recording — platform-owned until the recording feature ships with its own policy gate.",
	},
	{
		Name: "recording-name", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategorySession,
		Description: "Session recording — platform-owned (see recording-path).",
	},
	{
		Name: "create-recording-path", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool, Tier: TierPlatform, Category: CategorySession,
		Description: "Session recording — platform-owned (see recording-path).",
	},
	{
		Name: "typescript-path", Protocols: []string{"ssh"}, Kind: KindString, Tier: TierPlatform, Category: CategorySession,
		Description: "Terminal typescript recording — platform-owned (see recording-path).",
	},
	{
		Name: "sftp-hostname", Protocols: []string{"vnc", "rdp"}, Kind: KindString, Tier: TierPlatform, Category: CategoryConnection,
		Description: "SFTP side-channel to an arbitrary host — banned (the whole sftp-* family is unregistered on purpose).",
	},
}

// ForProtocol returns the parameters applying to one protocol, grouped
// by category (AllCategories order), UI tier before advanced before
// platform within a category, alphabetical within a tier. The frontend
// derives its section order from this payload order.
func ForProtocol(protocol string) []Param {
	catRank := map[Category]int{}
	for i, c := range AllCategories() {
		catRank[c] = i
	}
	tierRank := map[Tier]int{TierUI: 0, TierAdvanced: 1, TierPlatform: 2}
	var out []Param
	for _, p := range registry {
		for _, proto := range p.Protocols {
			if proto == protocol {
				out = append(out, p)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if catRank[out[i].Category] != catRank[out[j].Category] {
			return catRank[out[i].Category] < catRank[out[j].Category]
		}
		if tierRank[out[i].Tier] != tierRank[out[j].Tier] {
			return tierRank[out[i].Tier] < tierRank[out[j].Tier]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Protocols is the SINGLE SOURCE of protocol names, platform-wide.
// Every other list derives from or is guarded against it: the two CRD
// kubebuilder enums (kept in lockstep by TestCRDProtocolEnumsMatchTheRegistry),
// the remote-workspace validation, the api-server catalog validation
// and GET /meta/protocols. Add a protocol HERE; the guard test then
// walks you to the two enum markers.
//
// The first three are guacd protocols with tunable parameters; kasmvnc
// is the web-native KasmVNC path (kasmweb/* images), reverse-proxied by
// wwt instead of brokered by guacd — it has no guacd parameters, so
// every override is rejected by the registry gates (fail-closed).
func Protocols() []string { return []string{"vnc", "rdp", "ssh", "kasmvnc"} }

// Lookup finds one parameter for a protocol, or nil.
func Lookup(protocol, name string) *Param {
	for i := range registry {
		if registry[i].Name != name {
			continue
		}
		for _, proto := range registry[i].Protocols {
			if proto == protocol {
				return &registry[i]
			}
		}
	}
	return nil
}

// Violation explains one rejected parameter.
type Violation struct {
	Name    string
	Message string
}

func (v *Violation) Error() string { return fmt.Sprintf("parameter %q: %s", v.Name, v.Message) }

// ValidateValue checks one value against the parameter's kind and bounds.
func ValidateValue(p *Param, value string) *Violation {
	switch p.Kind {
	case KindBool:
		if value != "true" && value != "false" {
			return &Violation{p.Name, fmt.Sprintf("must be true or false, got %q", value)}
		}
	case KindInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return &Violation{p.Name, fmt.Sprintf("must be an integer, got %q", value)}
		}
		if p.Min != nil && n < *p.Min {
			return &Violation{p.Name, fmt.Sprintf("must be >= %d, got %d", *p.Min, n)}
		}
		if p.Max != nil && n > *p.Max {
			return &Violation{p.Name, fmt.Sprintf("must be <= %d, got %d", *p.Max, n)}
		}
	case KindEnum:
		for _, e := range p.Enum {
			if value == e {
				return nil
			}
		}
		return &Violation{p.Name, fmt.Sprintf("must be one of %v, got %q", p.Enum, value)}
	}
	return nil
}

// ValidateTemplateParams validates a template's locked params for one
// protocol: every name must be registered (unknown names are rejected —
// the registry is an allow-list, not a suggestion), must not be
// platform-owned, and values must be well-formed.
func ValidateTemplateParams(protocol string, params map[string]string) *Violation {
	for name, value := range params {
		p := Lookup(protocol, name)
		if p == nil {
			return &Violation{name, fmt.Sprintf("not a registered %s parameter (see docs/guacd-parameters.md to add one)", protocol)}
		}
		if p.Tier == TierPlatform {
			return &Violation{name, "platform-owned: " + p.Description}
		}
		if v := ValidateValue(p, value); v != nil {
			return v
		}
	}
	return nil
}

// ValidateUserParamNames validates a template's userParams allow-list:
// only registered, non-platform parameters may be delegated to users.
func ValidateUserParamNames(protocol string, names []string) *Violation {
	for _, name := range names {
		p := Lookup(protocol, name)
		if p == nil {
			return &Violation{name, fmt.Sprintf("not a registered %s parameter", protocol)}
		}
		if p.Tier == TierPlatform {
			return &Violation{name, "platform-owned parameters cannot be delegated to users"}
		}
	}
	return nil
}

// ValidateUserOverrides validates connect-time (or CR override) user
// values: names must be inside the template's allow-list AND registered
// non-platform, values must be well-formed. adminBypass skips the
// allow-list (platform admins may tune anything non-platform).
func ValidateUserOverrides(protocol string, overrides map[string]string, allowList []string, adminBypass bool) *Violation {
	allowed := map[string]bool{}
	for _, name := range allowList {
		allowed[name] = true
	}
	for name, value := range overrides {
		p := Lookup(protocol, name)
		if p == nil {
			return &Violation{name, fmt.Sprintf("not a registered %s parameter", protocol)}
		}
		if p.Tier == TierPlatform {
			return &Violation{name, "platform-owned: " + p.Description}
		}
		if !adminBypass && !allowed[name] {
			return &Violation{name, fmt.Sprintf("not user-configurable for protocol %s (template userParams: %v)", protocol, allowList)}
		}
		if v := ValidateValue(p, value); v != nil {
			return v
		}
	}
	return nil
}
