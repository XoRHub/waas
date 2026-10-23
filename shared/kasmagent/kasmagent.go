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
// may declare a protocol called "files"), and the guacd PulseAudio port
// is "audio". Kept as the files name when the audio routes joined: the
// port is the same, renaming it would only churn every Service.
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
// under /kasm/{sessionID}/waas/… and gates each on the connection token:
// the file routes on its file-transfer grant (list: upload or download,
// download, upload), the audio WebSockets on its audio grant (playback,
// microphone).
const (
	PathList     = "/files"
	PathDownload = "/files/download"
	PathUpload   = "/files/upload"
	PathAudio    = "/audio"
	PathMic      = "/mic"
)

// DefaultUploadDir is where uploads land when the request names no
// directory — the kasmweb convention (their own upload service writes to
// ~/Uploads).
const DefaultUploadDir = "Uploads"

// PulseDir is the pod-local emptyDir the operator mounts in BOTH the
// desktop and the sidecar: the sidecar's PulseAudio daemon listens on
// PulseSocket there and reads the microphone from PulseMicFIFO; the
// desktop's clients reach it through EnvPulseServer=PulseServer (an
// operator-owned env, refused in templates like the side-service
// toggles). Same uid in both containers, so no permission dance.
const (
	PulseDir     = "/run/waas-pulse"
	PulseSocket  = PulseDir + "/native"
	PulseMicFIFO = PulseDir + "/mic.fifo"
	// PulseSink/PulseSource are the daemon's only devices: a null sink
	// whose monitor the /audio route streams, and the pipe source the
	// /mic route feeds. Both are the defaults, so unconfigured desktop
	// apps land on them.
	PulseSink      = "waas_out"
	PulseSource    = "waas_mic"
	EnvPulseServer = "PULSE_SERVER"
	PulseServer    = "unix:" + PulseSocket
)

// Audio wire format on the /audio and /mic WebSockets: raw PCM, signed
// 16-bit little-endian, 48 kHz, in binary frames of AudioFrameMillis.
// Playback is stereo, the microphone mono.
// ponytail: raw PCM (≈1.5 Mbit/s out, ≈0.77 Mbit/s in); Opus when the
// bandwidth matters more than the zero-dependency path.
const (
	AudioRate        = 48000
	AudioChannels    = 2
	MicChannels      = 1
	AudioFrameMillis = 20
)
