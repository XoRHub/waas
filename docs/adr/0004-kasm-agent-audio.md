# ADR 0004 — Audio on KasmVNC: PulseAudio inside the kasm-agent sidecar

**Status**: accepted (2026-10-23). **Decider**: platform owner, on the
Lot 4 restitution of the KasmVNC support plan.

## Context

Standalone KasmVNC has no audio at all — playback and microphone are a
Kasm Workspaces (proprietary platform) exclusive. The `kasmweb/*` images
do ship the platform's helpers (`kasm_audio_out`, a PulseAudio + ffmpeg
pipeline into an MPEG-TS WebSocket, and `kasm_audio_input_server`), but
they are closed binaries of that platform: the operator keeps them off
(`KASM_SVC_AUDIO=0`, `KASM_SVC_AUDIO_INPUT=0`, `kasmcfg.SideServiceEnv`),
and running them is exactly what the "never install a Kasm agent" rule
forbids. With `KASM_SVC_AUDIO=0` the image does not even start its own
PulseAudio (`pulseaudio --start` is nested in that service's block of
`vnc_startup.sh`), so a kasmvnc desktop has no sound server, and apps
inside it have nowhere to play.

ADR 0003 put a WaaS-owned sidecar next to every kasmvnc desktop for file
transfer and announced that audio would extend it.

## Decision

**PulseAudio lives in the sidecar, not in the desktop.** The kasm-agent
supervises one `pulseaudio` daemon (restart with backoff) loading three
modules and nothing else: `module-native-protocol-unix` on a socket in a
small Memory emptyDir the operator mounts in **both** containers
(`/run/waas-pulse`, `kasmagent.PulseDir`), `module-null-sink` (`waas_out`,
s16le 48 kHz stereo — the default sink) and `module-pipe-source`
(`waas_mic`, a FIFO in the same emptyDir, s16le 48 kHz mono — the default
source). The desktop's audio clients reach it through `PULSE_SERVER=unix:…`,
an operator-owned env stamped with the side-service toggles and refused
in templates like them (`kasmcfg.ManagedEnv`). Both containers run as the
desktop's uid, so the socket and FIFO need no permission dance;
`auth-anonymous=1` because the only path to the socket is the pod.

**Two WebSocket routes on the agent**, behind the same Basic pair as the
file routes: `GET /audio` streams the null sink's monitor (one `parec`
per connection, killed when the socket closes) as raw PCM s16le 48 kHz
stereo in 20 ms binary frames; `GET /mic` writes the browser's raw PCM
s16le 48 kHz mono frames into the pipe source's FIFO. One mic writer at
a time: a second `/mic` connection is refused with 409 while one is
open — the simplest rule that keeps two streams from interleaving, and
the portal only ever opens one. Raw PCM is a deliberate ceiling
(≈1.5 Mbit/s out, ≈0.77 Mbit/s in, `ponytail:` comments mark it): Opus is
the upgrade when bandwidth matters more than the zero-dependency path.

**Gate in wwt, mirror of file transfer.** `WorkspacePolicy.audio
{playback, microphone}`, default deny, resolved for the connecting user
at `/connect` and stamped into the connection token (`audio` claim) and
the session capabilities (`audioPlayback`, `microphone`); wwt's agent
gate (`agentRouteStatus`, the renamed `fileRouteStatus`) requires
`playback` on `/waas/audio` and `microphone` on `/waas/mic`, one method
each, before relaying the upgrade through the same `proxyTo`. The audio
sockets are **not** desktop streams: they neither keep a session alive
nor end it (the reconnect-grace counting is skipped on the agent
branch).

**Defaults.** The chart's bootstrap default policy grants `playback`
(plain usability, no egress) and not the microphone (a live capture of
the user's surroundings, opt-in); the admin policy and the gitops
`admins` policy grant both.

**Base image: Debian 13 slim, PulseAudio copied in.** The sidecar was
distroless; it needs `pulseaudio` and `parec` now. Alpine's build was
tried first and rejected: its `module-pipe-source` aborts on every FIFO
read (`pa_frame_aligned` assertion, pulseaudio 17.0-r5 on 3.23, every
sample spec), and the package drags gstreamer, mesa and LLVM (≈265 MB)
anyway. Debian's package works but drags ffmpeg and the ALSA plugins
(≈265 MB too), so a build stage installs it and the final `debian:13-slim`
stage receives only the daemon, the three modules (plus
`libprotocol-native`), `parec` and the shared libraries `ldd` resolves
for them: **105 MB**. Same base digest in both stages, so a library
copied over itself is a no-op.

**WebSocket library.** `gorilla/websocket`, already in wwt's `go.mod`,
at the same version — the agent is no longer stdlib-only.

**Frontend.** One module, `lib/kasmAudio.ts` + `lib/pcmWorklet.js`: an
`AudioContext` at 48 kHz per link, an `AudioWorklet` player with a small
drop-oldest jitter buffer (prime 60 ms, cap 300 ms) and a capture
posting 20 ms blocks. The worklet is a plain JS file shipped as a hashed
same-origin asset (`?url&no-inline`), so the nginx CSP (`script-src
'self'`) is untouched; the WebSockets are covered by the existing
`connect-src ws(s)://$http_host`. The session cookie is refreshed before
each socket like the Files section does. The KasmVNC client posts
`enable_audio` to its parent on every click in the desktop; the playback
link treats it as the user gesture that resumes a context the browser's
autoplay policy kept suspended.

## Consequences

- Speaker and microphone toggles in the kasmvnc session menu, each shown
  only when the policy grants it; errors are worded (refused capture,
  stream closed, sidecar unavailable). The microphone needs a secure
  context (`getUserMedia`): HTTPS, or the dev env's 8443 port.
- Bandwidth: a playback link costs ≈1.5 Mbit/s **for as long as it is
  on**, silence included (the null sink's monitor never stops); a mic
  link ≈0.77 Mbit/s. Fine on a LAN, the reason Opus is the next step.
- Soft vs governed, as for files: the grant holds for *this* path, which
  the occupant cannot modify; the shared emptyDir is theirs too (same
  uid), so they can unplug the socket or feed the FIFO from inside — a
  denial of their own audio, not an escalation.
- The sidecar's footprint grows (20m/32Mi requests, 128Mi limit; one
  `parec` per playback stream) and every running kasmvnc workspace
  drifts at this release like any sidecar image bump (ADR 0001).
- Trivy sees the final image's Debian packages, not the copied PulseAudio
  libraries (no dpkg entries): they follow the base digest bump but are
  not scanned as packages. Known trade-off against a 265 MB sidecar.
- Known log noise: pulseaudio 17 logs a memfd fallback
  (`Cannot send block reference with non-registered memfd ID`, upstream
  issue 824) on the first stream; harmless.
- `rdp`/`vnc` (guacd) audio is unchanged (`enable-audio` +
  `exposeAudioPort`, PulseAudio in waas-core-images).
