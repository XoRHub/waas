// Package kasmagent is the contract between the kasm-agent sidecar (which
// serves it), the operator (which injects the sidecar next to kasmvnc
// desktops) and wwt (which proxies the browser to it behind the policy
// gate). The three live in separate Go modules, so the port, env and
// route names are kept here once instead of three literals that drift.
package kasmagent

// Port is the sidecar's fixed listen port: plain HTTP inside the pod
// network, the same posture as the kasmvnc hop (docs/kasmvnc.md,
// "Upstream TLS posture").
const Port int32 = 4800

// PortName names the port on the sidecar container and the workspace
// Service. Prefixed: the protocol port names are the admin's (a template
// may declare a protocol called "files"), and an audio port is "audio".
const PortName = "waas-files"

// EnvPassword is the kasmweb images' own password variable. The sidecar
// accepts the exact Basic pair wwt already injects for KasmVNC
// (kasm_user:$VNC_PW), so the existing per-workspace Secret is the only
// credential — nothing new to generate or rotate.
const EnvPassword = "VNC_PW"

// EnvMaxUploadBytes caps one upload request's body; absent, the sidecar
// applies DefaultMaxUploadBytes.
const EnvMaxUploadBytes = "KASM_AGENT_MAX_UPLOAD_BYTES"

// DefaultMaxUploadBytes is 1 GiB.
const DefaultMaxUploadBytes int64 = 1 << 30

// Routes, relative to the sidecar's root. wwt serves them to the browser
// under /kasm/{sessionID}/waas/… and gates each on the connection token's
// file-transfer grant (list: upload or download, download, upload).
const (
	PathList     = "/files"
	PathDownload = "/files/download"
	PathUpload   = "/files/upload"
)

// DefaultUploadDir is where uploads land when the request names no
// directory — the kasmweb convention (their own upload service writes to
// ~/Uploads).
const DefaultUploadDir = "Uploads"
