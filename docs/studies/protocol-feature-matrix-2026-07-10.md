# Protocol × feature matrix (2026-07-10)

Sourced status report of the four connection paths (VNC, RDP, SSH —
brokered via guacd — and KasmVNC — raw HTTP reverse-proxy via
`wwt/internal/kasm`, without guacd), cross-referenced against the
cross-cutting features. Every cell is verified either in the code
(file:line) or in existing documentation; nothing is estimated. This
document does not repeat what
[templates-and-protocols.md](../templates-and-protocols.md) already
documents (template/overrides model, form tiers, credentials) — it
references it.

**Sources of truth and freshness**:

- The `operator/pkg/params/params.go` registry (every `Param` carries
  `Protocols`, `Tier`, `Live`, `Default`) is the single source;
  `docs/guacd-parameters.md` is generated from it. Regenerated on
  2026-07-10 (`make docs-params`): **no diff**, the file was up to
  date.
- `kasmvnc` is in `Protocols()` (`params.go:447`) but **no registry
  entry lists it in `Protocols`**: any guacd parameter set on a
  kasmvnc protocol is rejected fail-closed (`Lookup` returns nil →
  violation, `params.go:450-462, 505-519`). The only kasmvnc
  configuration channel is `spec.kasmvncConfig` (opaque admin-only
  YAML, never parsed —
  [templates-and-protocols.md](../templates-and-protocols.md)
  §KasmVNC).
- Runtime behaviors not testable without a cluster: marked ❓ with the
  reason, never guessed.

**Legend**: ✅ `ui` = exposed in the portal forms · ⚙️ `advanced` =
CR/YAML or advanced section only · 🚫 `platform` = blocked at the
platform level (reason in the registry's `Description`) · ❌ =
nonexistent on this path · N/A = not applicable (e.g. SSH terminal) ·
❓ = not verifiable without a live session. An asterisk points to the
notes below the table.

## Main table

| Feature | VNC | RDP | SSH | KasmVNC |
|---|---|---|---|---|
| 1. Audio — playback | ✅ ui `enable-audio` *[1]* | ✅ ui `disable-audio` (guacd default: on) *[1]* | N/A | ❌ *[2]* |
| 2. Audio — microphone | ❌ (no parameter) | ⚙️ advanced `enable-audio-input`, **inert on the web client side** *[3]* | N/A | ❌ *[2]* |
| 3. Governed clipboard (policy + live) | ✅ ui, `Live` *[4]* | ✅ ui, `Live` — channel absent from internal images *[4]* | ✅ ui, `Live` *[4]* | ❌ **not governed** *[5]* |
| 4. Persistent home | ✅ (protocol-independent) *[6]* | ✅ *[6]* | ✅ *[6]* | ✅ (`/home/kasm-user`) *[6]* |
| 4b. Concurrent shared volume | ❌ *[6]* | ❌ *[6]* | ❌ *[6]* | ❌ *[6]* |
| 5. File transfer | 🚫 platform *[7]* | 🚫 platform *[7]* | 🚫 platform *[7]* | ❌ *[7]* |
| 6. Session recording | 🚫 platform *[8]* | 🚫 platform *[8]* | 🚫 platform *[8]* | ❌ *[8]* |
| 7. Keyboard layout | N/A (direct keysyms) *[9]* | ✅ ui `server-layout` + auto-detection *[9]* | N/A (direct keysyms) *[9]* | N/A *[9]* |
| 8. Dynamic resize | ❌ *[10]* | ✅ ui `resize-method`, **inert today** *[10]* | ❌ (size fixed at handshake) *[10]* | ✅ native, exploited *[10]* |
| 9. Multi-monitor | ❌ | ❌ | N/A | ❓ *[11]* |
| 10. Image quality / color | ✅ ui ×4 *[12]* | ✅ ui + ⚙️ advanced ×9 *[12]* | ✅ ui (terminal look) *[12]* | ⚙️ via `kasmvncConfig` only *[12]* |
| 11. Live parameters (`Live`) | `disable-copy`/`disable-paste` only *[13]* | same *[13]* | same *[13]* | ❌ none *[13]* |
| 12. Post-creation workspace overrides | ✅ implemented (Feature 1) *[14]* | ✅ *[14]* | ✅ *[14]* | ✅ *[14]* |

Notes (sources):

1. `enable-audio`: `params.go:116` (ui, VNC); `audio-servername`:
   `params.go:121` (advanced). `disable-audio`: `params.go:160` (ui,
   RDP); `console-audio`: `params.go:250` (advanced). **But** no
   internal image provides audio: `waas-images/HARDENING.md:78-82`
   ("Audio is not shipped", no PulseAudio, no xrdp chansrv). See
   §Audio.
2. Capability "Kasm platform only", out of scope for standalone:
   `docs/studies/kasm-images-feasibility.md:42-43` — still accurate:
   `wwt/internal/kasm/kasm.go` is a pure reverse-proxy, with no audio
   stream added.
3. `enable-audio-input`: `params.go:165` (advanced, RDP). No
   client-side microphone capture: zero occurrence of
   `AudioRecorder`/`createAudioStream` in `frontend/src` (verified by
   grep on 2026-07-10). See §Audio.
4. `disable-copy`/`disable-paste`: `params.go:84,89` (ui, `Live:
   true`, VNC+RDP+SSH). wwt enforcement: `wwt/internal/guac/clipboard.go`
   (stream dropping + live toggles clamped to the grant). Internal
   image RDP limitation: `waas-images/HARDENING.md:78-79`. See
   §Clipboard.
5. No equivalent of the `ClipboardFilter` on the kasm path:
   `wwt/internal/kasm/kasm.go` proxies bytes and WebSocket without
   inspection (`proxyTo`, `kasm.go:167-202`). v1 decision, knowingly
   accepted (`kasm-images-feasibility.md:86`). See §Clipboard and
   §Gaps.
6. `docs/volumes.md` (PVC = source of truth, retention by labels); RWO
   + `Recreate` strategy forbid double-mounting
   (`templates-and-protocols.md` §Workload). `homeVolumeName` = adoption
   of a **retained** volume at creation (`volumes.md:31-35`), not
   concurrent sharing. See §Volumes.
7. `enable-sftp`: `params.go:365`; `enable-drive`: `params.go:369`;
   `sftp-hostname` banned: `params.go:409` — all `TierPlatform`,
   reason: "until the file-transfer feature ships with its own policy
   gate". KasmVNC: upload/download = Kasm platform only
   (`kasm-images-feasibility.md:42-43`). See §Transfer & recording.
8. `recording-path`/`recording-name`/`create-recording-path`:
   `params.go:393-403`; `typescript-path` (SSH): `params.go:405` — all
   `TierPlatform`, same reason of awaiting a policy gate. No recording
   mechanism on the kasm path (nothing in `wwt/internal/kasm`).
9. `server-layout`: `params.go:172-183` (ui, enum with 24 values);
   local browser auto-detection: `DesktopPane.tsx:175` (`?layout=`) +
   `wwt/internal/guac/handshake.go:120-127`. VNC/SSH with no
   equivalent: `templates-and-protocols.md` §Keyboard layout ("VNC
   forwards keysyms directly") — confirmed: no VNC/SSH layout
   parameter in the registry.
10. `resize-method`: `params.go:145` (ui, RDP). Inert in practice: see
    §Resize and §Gaps. KasmVNC: `resize=remote` passed to the embedded
    client (`DesktopPane.tsx:158`), native dynamic resize confirmed by
    the PoC (`kasm-images-feasibility.md:98`).
11. Standalone KasmVNC capability per the study
    (`kasm-images-feasibility.md:42-43`); no explicit WaaS integration
    (nothing in `DesktopPane.tsx` nor `wwt/internal/kasm`). The full
    KasmVNC client is embedded in an iframe, its own UI may expose it —
    **not verified without a live session** (secondary windows + cookie
    scoped to `/kasm/{sid}`: behavior unknown).
12. VNC: `color-depth` (`params.go:96`), `swap-red-blue` (`:101`),
    `cursor` (`:106`), `force-lossless` (`:111`) — all ui. RDP:
    `color-depth` ui + 9 cosmetic/perf advanced settings
    (`params.go:190-233`). SSH: `font-size` (`:262`), `color-scheme`
    (`:267`) ui. KasmVNC: nothing in the registry; `VNC_RESOLUTION` →
    `MaxVideoResolution` (`kasm-images-feasibility.md:98`) and upstream
    options via `kasmvncConfig` (admin, opaque, not validated).
13. `grep 'Live: true' params.go` → lines 85 and 90 only (verified on
    2026-07-10): no other live parameter has been added. kasm path: no
    guac tunnel, therefore no `waas-clipboard` messages — no live
    parameter.
14. Feature 1 delivered: commits `c671cbd40f55` (PATCH
    `/workspaces/{id}/overrides` + reload endpoint), `320d746ecaaf`
    (operator one-shot reload + ADR), `4a9f41a910c4` (Workspace tab).
    See §Post-creation overrides.

## 1-2. Audio — the registry promises, the images don't follow through

Three layers need to align; today **no first-party combination
produces sound**:

- **Registry/forms**: `enable-audio` (VNC) and `disable-audio` (RDP)
  are `Tier: ui` — the portal offers them in simple mode
  (`params.go:116,160`; generic rendering documented in
  `templates-and-protocols.md` §Parameter forms).
- **Web client**: playback would work with no specific code —
  `guacamole-common-js` instantiates an `AudioPlayer` by default when
  `client.onaudio` isn't set
  (`frontend/node_modules/guacamole-common-js/dist/esm/guacamole-common.js:2887-2892`).
  The **microphone**, on the other hand, requires explicit wiring
  (`Guacamole.AudioRecorder` + `createAudioStream`) that exists
  nowhere in `frontend/src`: `enable-audio-input` (`params.go:165`)
  can't capture anything, even if guacd and the RDP server accepted
  it.
- **Images**: `waas-images/HARDENING.md:80-82` — audio isn't bundled
  (`pulseaudio-module-xrdp` not packaged for Ubuntu; no PulseAudio
  reachable for the VNC path). The registry's description is honest
  ("requires the image to run one", `params.go:118`) but no image in
  the internal catalog satisfies the condition.

**KasmVNC**: the state described in the feasibility study is
unchanged — audio (and microphone) are functions of the Kasm
*platform*, not of the standalone server (`kasm-images-feasibility.md:42-43,54`:
never install the Kasm agent, that's the licensing boundary).
`wwt/internal/kasm` adds nothing. **SSH**: N/A, guacd only renders a
terminal.

A session with real audio would therefore require: a third-party image
with reachable PulseAudio (VNC) — **not verifiable without a cluster
nor a third-party image**, marked as such.

## 3. Clipboard — governed everywhere except kasm

[clipboard.md](../clipboard.md) documents the full chain (policy →
JWT grant → wwt `ClipboardFilter` → client) and its browser matrix;
everything below confirms it, with two reconciliations:

- **The clipboard.md matrix remains accurate** for VNC/SSH (both
  directions, wwt enforcement independent of the protocol,
  `wwt/internal/guac/clipboard.go:39-92`, tested in
  `clipboard_test.go`). The overlay toggles are live and clamped to
  the grant (`DesktopPane.tsx:92-94` → `waas-clipboard` messages).
- **RDP, to be hardened in clipboard.md**: the doc says "xrdp without
  sesman does not *always* mount the cliprdr channel"
  (`clipboard.md:66-67`); `waas-images/HARDENING.md:78-79` is more
  categorical — *no chansrv at all* → never any RDP clipboard with the
  internal image. The nuance matters for **remote workspaces**: a real
  RDP server (Windows) mounts cliprdr, and the wwt filter then applies
  normally (same tunnel, same token — `docs/remote-workspaces.md:108-109`).
- **KasmVNC: nothing is governed.** Verified in
  `wwt/internal/kasm/kasm.go`: the handler validates the JWT, resolves
  the session, then proxies everything (`httputil.ReverseProxy`,
  `kasm.go:167-202`) — no flow inspection, no equivalent of the
  `ClipboardFilter`. The clipboard works through KasmVNC's own UI
  inside the iframe, **out of the policy's reach**. This is the v1
  decision, knowingly accepted (`kasm-images-feasibility.md:86`),
  mitigation planned for phase 4 (KasmVNC DLP derived from the
  policy). But the "honest display" that was supposed to accompany
  this decision isn't there — see §Gaps.
- Related parameters: `clipboard-encoding` (VNC, advanced,
  `params.go:136`) and `normalize-clipboard` (RDP, advanced,
  `params.go:235`) exist but don't change the governance.

## 4. Volumes — persistent home yes, sharing no

The complete model is in [volumes.md](../volumes.md) (PVC = source of
truth, retention by labels, quotas). For the matrix:

- The **home PVC is mounted on all in-cluster paths**, kasmvnc
  protocol included (`homeMountPath` `/home/kasm-user` for Kasm
  images, persistence validated in the PoC —
  `kasm-images-feasibility.md:101`). Row 4 is identical across all 4
  columns because the volume is a property of the workspace, not of
  the protocol.
- There is **no volume shared between workspaces**: the PVC is RWO
  with a `Recreate` strategy precisely so that two pods never overlap
  (`templates-and-protocols.md` §Workload), and `homeVolumeName` only
  allows **adopting a `retained` volume** (previous workspace deleted)
  by its owner, within the same namespace (`volumes.md:31-35`).
  Sequential adoption ≠ concurrent sharing.
- **Remote workspaces**: N/A — no storage resource on the platform
  side (`model.go:82-86`: "no template, no operator lifecycle, no
  compute").

## 5-6. File transfer and recording — deliberately blocked

To be said unambiguously: **not supported today**, on any path, for
anyone (admins included — the `platform` tier is rejected "whoever
asks", `params.go:34-37`).

- Transfer: `enable-sftp` (VNC/RDP/SSH) and `enable-drive` (RDP) are
  `TierPlatform` with the explicit reason "until the file-transfer
  feature ships with its own policy gate" (`params.go:365-371`); the
  entire `sftp-*` family is furthermore deliberately unregistered,
  with `sftp-hostname` banned as a side-channel (`params.go:409-411`).
  These are **not** "available in advanced" parameters: the webhook
  (`workspacetemplate_webhook.go:63-67`), the connect
  (`workspace_service.go:548`), and the remote path
  (`remote_workspace_service.go:186,398`) all reject them via the same
  registry.
- Recording: `recording-path`, `recording-name`,
  `create-recording-path` (VNC/RDP/SSH) and `typescript-path` (SSH) —
  same status, same reason (`params.go:393-407`).
- KasmVNC: no equivalent (upload/download = Kasm platform only,
  `kasm-images-feasibility.md:42-43`) — consistent with the guacd-side
  block: no file leak on any path.

## 7. Keyboard — RDP only, and it's structural

Already documented in detail in `templates-and-protocols.md` §Keyboard
layout (auto); for the matrix: only RDP negotiates a layout
(`server-layout`, ui, enum of 24 layouts + `failsafe`,
`params.go:172-183`), with a default auto-detected from the browser
locale (`DesktopPane.tsx:175` → `handshake.go:120-127`). VNC and the
SSH terminal transmit keysyms — the layout is that of the X server /
the shell, there is **nothing to configure on the guacd side**
(verified: no VNC/SSH layout parameter in the registry). KasmVNC: same
keysym logic on the embedded client side, no platform parameter.

## 8. Resize — the only real success is kasm

Verified state of the three mechanisms:

- **guacd path (VNC/RDP/SSH)**: the display size is fixed once at
  handshake time — the client sends `width`/`height`/`dpi` as
  WebSocket query params (`DesktopPane.tsx:167-171`), wwt translates
  them into a `size` instruction (`handshake.go:54-65`, defaults
  1920×1080@96). After that, the pane's `ResizeObserver` only
  **rescales the canvas client-side** (`DesktopPane.tsx:117-125,292-293`);
  no `client.sendSize()` anywhere (`grep sendSize frontend/src wwt`:
  zero occurrences, 2026-07-10). Consequences:
  - VNC: no dynamic resize, and that's consistent — no registry
    parameter, the geometry is that of the Xvnc server;
  - RDP: `resize-method` (ui, `params.go:145`) chooses how guacd
    *would propagate* a resize… which is never emitted → parameter
    inert today (see §Gaps);
  - SSH: the terminal stays at the handshake size, scaled via CSS.
- **KasmVNC**: the native dynamic resize (`AcceptSetDesktopSize`) is
  indeed **exploited**: the iframe is mounted with `resize=remote`
  (`DesktopPane.tsx:156-163`), the KasmVNC client drives the actual
  geometry (PoC: `kasm-images-feasibility.md:98`). This is today the
  only path where the browser window actually resizes the desktop.

## 9. Multi-monitor — remained theoretical

No trace on the WaaS side: neither `DesktopPane.tsx` (one pane = one
canvas or one iframe), nor `wwt/internal/kasm`, nor the split view
(which juxtaposes *sessions*, not the monitors of a single session —
`templates-and-protocols.md` §Portal UX). The capability cited by the
feasibility study (`kasm-images-feasibility.md:42-43`) is that of the
upstream KasmVNC client, entirely embedded in the iframe: its internal
UI may offer opening secondary monitors, but that goes through popup
windows whose interaction with the cookie scoped to `Path=/kasm/{sid}`
(`kasm.go:136-143`) has never been exercised — **❓ not verified, to be
tested in a live session before any promise is made**.

## 11. Live parameters — two, and only two

`grep 'Live: true'` on the registry (2026-07-10): `params.go:85` and
`params.go:90` — `disable-copy` and `disable-paste`, nothing else
since their introduction. The mechanism (`waas-clipboard` tunnel
messages intercepted by wwt, clamped to the grant) only concerns the
guac tunnel; the kasm path has neither a tunnel nor a live parameter.
Everything else requires a reconnection (guacd freezes parameters at
connect time, `params.go:66-68`) — the overlay indeed distinguishes
"live" settings from "reconnect" settings
(`SessionOverlay.tsx:113-117,249-264`).

## 12. Post-creation workspace overrides — Feature 1 delivered

This study is written **after** the implementation of Feature 1
(`docs/studies/prompt-feature1-workspace-runtime-config.md`); the
actual status:

- `PATCH /api/v1/workspaces/{id}/overrides` accepts `env`,
  `nodeSelector`, `tolerations`, `resources` ("presence = replacement"
  semantics, `workspace_service.go:384-397`); permissions are still
  judged by the webhook alone (template ∩ policy intersection,
  re-denied as a 403 — `workspace_service.go:399-406`), audit
  `workspace.overrides_updated` (env names only, never the values).
- Application to the pod follows ADR 0001
  (`docs/adr/0001-template-boundary-convergence.md`): convergence at
  scale-up boundaries (pause/resume), never mid-session; in the
  meantime, `TemplateDrifted` condition + clickable badge →
  **confirmed manual reload** (operator one-shot annotation, commit
  `320d746ecaaf`; UI commit `4a9f41a910c4`, "Workspace" tab of
  Connection settings, `frontend/src/dialogs/WorkspaceRuntimeForm.tsx`).
- Protocol-independent (it's the workload that changes), hence ✅
  across all 4 columns. Not to be confused with post-creation
  **connection params**, which have always been possible at connect
  time (`templates-and-protocols.md` §SSH, `/connect` point).

## Remote workspaces (`RemoteWorkspace`) — same protocols, lighter governance

Full model in [remote-workspaces.md](../remote-workspaces.md);
differences relevant to the matrix (`kind: "remote"` sessions,
`model.go:61`):

| Aspect | In-cluster | Remote |
|---|---|---|
| Access to the feature | always | opt-in `remoteWorkspaces` policy, fail-closed (`remote_workspace_service.go:142-159`, `model.go:390-392`) |
| Parameter tiers | non-admins bounded to the template's `userParams` (`workspace_service.go:548`) | **ui + advanced free** for the owner — no template, only the `platform` barrier applies (`remote_workspace_service.go:186,396-399`) |
| Clipboard | policy + wwt filter | **identical** — same grant resolved (`remote_workspace_service.go:417`), same tunnel, same filter; and on a real RDP server the cliprdr channel exists, unlike the internal images |
| Transfer / recording | 🚫 platform | 🚫 platform (same registry) |
| Wake-on-LAN | `wol-send-packet` banned ("meaningless in-cluster", `params.go:373`) | ✅ supported via a different mechanism: external HTTP relay + `macAddress` (`remote_workspace_service.go:479-499`, `docs/remote-workspaces.md` §WoL) |
| Home / volumes / runtime overrides | ✅ | N/A (nothing instantiated — `docs/frontend-capabilities.md`: no Workspace tab, no drift badge) |
| KasmVNC | ✅ (nominal kasmweb path) | **accepted by the code, undocumented**: validation takes the whole `params.Protocols()` so kasmvnc included (`remote_workspace_service.go:174`), resolution applies `kasmDefaults` (`workspace_service.go:810`) and the frontend routes `kind=remote` + `kasmvnc` to the iframe (`DesktopPane.tsx:134-135,149`). But `remote-workspaces.md:4-5` and the model comment (`model.go:82-84` "reachable through guacd") say ssh/vnc/rdp. **❓ never exercised** (the smoke test doesn't cover this crossover) — to be settled: either document it as supported, or reject it at registration |

## Gaps vs. what the UI implies

In decreasing order of severity:

1. **Clipboard overlay on a kasmvnc session: shown, inoperative, and
   not enforced.** The overlay's clipboard section renders for every
   session, with no distinction by protocol
   (`SessionOverlay.tsx:244-264`): "live" toggles clamped to the
   policy capabilities, manual exchange. On the kasm path, (a) the
   toggles call `setClipboard` → `tunnelRef.current` is nil → silent
   no-op (`DesktopPane.tsx:92-94`), (b) manual exchange goes through
   `sendClipboardRef`, left at its default no-op
   (`DesktopPane.tsx:59` — never reassigned on the kasm branch), and
   (c) the real clipboard lives inside the KasmVNC iframe, **outside
   the policy's reach** (§Clipboard). The UI thus suggests governance
   that doesn't exist — the exact opposite of the "honest display"
   committed to in `kasm-images-feasibility.md:86`.
2. **`resize-method` (RDP, ui tier) is a choice with no effect.** The
   form offers display-update vs reconnect (`params.go:145-147`), but
   the client never emits a resize mid-session (no `sendSize`,
   §Resize): both values are indistinguishable today. Either wire up
   `sendSize` on the `ResizeObserver` (`DesktopPane.tsx:292`), or
   downgrade the parameter.
3. **The audio toggles (ui tier) produce no sound with the internal
   catalog.** `enable-audio` (VNC) and `disable-audio` (RDP) appear in
   simple mode while no internal image has an audio chain
   (`HARDENING.md:78-82`); `enable-audio-input` is furthermore inert
   on the browser side (§Audio). Since the form is generated from the
   registry, there is no lie *in the code* — but the user ticks a box
   that can't do anything.
4. **RDP clipboard: two docs that don't say the same thing.**
   `clipboard.md:66-67` ("doesn't always mount cliprdr") vs
   `HARDENING.md:78-79` (no chansrv → never). To be reconciled in
   clipboard.md, while keeping the remote-RDP nuance where the filter
   genuinely serves a purpose (§Clipboard).
5. **Remote + kasmvnc: real capability undocumented** (§Remote
   workspaces table) — the reverse case of the previous points: the
   code does more than the docs claim, with no test protecting it.
6. Minor — **kasmvnc forms empty by construction**:
   `ForProtocol("kasmvnc")` returns nothing (`params.go:416-434`), so
   the portal's protocol tabs display no parameter for kasmvnc; the
   real configuration (quality, DLP, SSL) goes through the opaque
   admin-only `kasmvncConfig`. Consistent, but worth knowing: the
   portal's "advanced mode" will never show anything for this protocol
   until phase 4 ("kasmvnc params registry",
   `kasm-images-feasibility.md:115`) exists.
7. **kasmvnc governance gap — resolved (Feature 11, 2026-07-10).**
   External research had revealed that standalone KasmVNC exposes a
   nine-endpoint `/api/…` API (all "owner credentials") that the
   `wwt/internal/kasm/kasm.go` proxy relayed unfiltered. The live
   audit confirmed the facts (see `kasm-images-feasibility.md`
   §"2026-07-10 update"); the decision and delivery:
   - **`/api/downloads` blocked** at the wwt proxy level (403), parity
     with `enable-sftp`/`enable-drive` frozen at `TierPlatform` on
     guacd "until the file-transfer feature ships with its own policy
     gate" (`params.go:365-371`). Verified live: listing and
     sub-paths → 403.
   - **The eight other endpoints remain proxied** (documented status
     quo): a single-user session that administers itself, `kasm_user`
     is `owner` (`wo` flags in `.kasmpasswd`), no third party to
     protect against the user themselves — they already have keyboard
     + screen.
   - **Clipboard genuinely governed**: the operator derives the DLP
     directives (`data_loss_prevention.clipboard.{server_to_client,
     client_to_server}.enabled` + `allow_client_to_override_kasm_server_settings:
     false`) from `WorkspacePolicy.Clipboard` and merges them into
     `kasmvnc.yaml` (`operator/internal/controller/kasm_config.go`).
     Denying clipboard genuinely disables copy/paste inside the
     container; the overlay displays the applied state (read from
     `capabilities`), no more static text. Verified live (deny →
     `enabled: false`).
   Original prompt: `08-prompt-feature11-kasmvnc-governance-gap.md`.
