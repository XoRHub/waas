# Feasibility study — Support for Kasm workspace images (`kasmweb/*`)

*2026-07-08 — study validated, phase 0 PoC passed (GO). Decisions
locked in: direct Docker Hub (no private mirror), amd64-only images
accepted, hybrid architecture chosen, ungoverned clipboard acceptable
in v1 with honest display.*

## Goal and problem statement

Extend the image catalog with the official Kasm images
(containerized desktops/apps), **without using the Kasm Workspaces
platform** (proprietary broker/UI) — WaaS remains the broker. The
crux: these images bundle **KasmVNC**, which has broken with RFB and
is only accessible via browser (HTTPS websocket, port 6901,
self-signed cert, Basic auth). **guacd cannot connect to it.**

## Audit of the existing setup (coupling points)

| Layer | State | guacd coupling |
|---|---|---|
| CRDs | `WorkspaceProtocol{name,port,params,userParams}`, enum `vnc;rdp;ssh` (template **and** image) | Enum + params registry 100% guacd |
| Data plane | browser (guacamole-common-js) → ingress `/ws` → wwt (JWT/JWKS, session→`ConnectionInfo`) → dial `WWT_GUACD_ADDR` | wwt speaks ONLY the guacamole tunnel |
| Auth | generic go-oidc (`WAAS_OIDC_*`) — **already IDP-agnostic** | none |
| Operator | Service exposes the protocols' ports; home mounted at `/home/user` (constant); `Architectures` → nodeAffinity already in place; volumes/securityContext passthrough | low |
| Governance | Clipboard policy enforced **by wwt's ClipboardFilter** | bypassed if we bypass the tunnel |
| Sessions | closed by wwt callback + SessionSweeper | end-of-session detected by wwt |

Four points to address: protocol enum, home path, clipboard policy,
end-of-session detection. The rest is reusable as-is.

## KasmVNC verifications (current state, verified)

- **1.4.0** (Oct 2025). Browser only ("does not support legacy VNC
  viewer applications"). YAML config `/etc/kasmvnc/kasmvnc.yaml` +
  `~/.vnc/kasmvnc.yaml`; `network.websocket_port` (6901 in the
  images), `network.ssl.require_ssl: true`, Basic auth via
  `kasm_password_file` (`kasmvncpasswd`).
- Standalone images (documented): `docker run --shm-size=512m -p
  6901:6901 -e VNC_PW=... kasmweb/...` → `https://<ip>:6901`, user
  `kasm_user`. No platform dependency.
- **Standalone**: clipboard, dynamic resize, multi-monitor, server-side
  DLP. **Platform only**: audio, mic. **Corrected 2026-07-10** (see
  §Audit of the kasmvnc API below): file download (session → browser)
  is NOT platform-only — it's a standalone capability (`/api/downloads`)
  that WaaS was already exposing without knowing it. Only upload
  (browser → session) is genuinely absent from KasmVNC, at every scale
  (neither standalone nor platform implements it according to the
  upstream maintainer).
- **ARM64 (Docker Hub, tags 1.18/1.19)** — multi-arch: `core-ubuntu-*`,
  `ubuntu-noble-desktop`, `firefox`, `vs-code`, `terminal`;
  amd64-only: `desktop`, `chrome` (accepted, existing nodeAffinity
  placement).

## Licenses (texts verified)

| Artifact | License | Rights in our context | Obligations / risks |
|---|---|---|---|
| KasmVNC (server) | **GPL-2.0** | Unlimited execution (including commercial): GPL only restricts distribution | Public redistribution of images containing KasmVNC ⇒ GPLv2 §3 (source). Modification ⇒ modified sources. **We do neither ⇒ zero obligation** |
| `workspaces-images` / `workspaces-core-images` repos | **MIT** text + disclaimer: covers only the Dockerfiles/scripts, NOT the embedded dependencies | Copy/adapt the Dockerfiles | The built image is an aggregate (MIT + GPL + Ubuntu + app EULAs — Chrome). Docker Hub pull: OK. Public republication: to avoid |
| Kasm Workspaces platform | Proprietary; Community = 5 concurrent sessions, non-commercial | **No component used** — the standalone mode is documented with no dependency or phone-home | None. Never install a Kasm agent (audio/upload): that's what would fall under their license |

## Architecture chosen (hybrid)

- **guacd left intact** for our own images (VNC/RDP/SSH).
- New **`kasmvnc`** protocol implemented by **wwt in raw
  reverse-proxy mode**: platform JWT auth (unchanged, IDP-agnostic),
  dial `pod:6901` over TLS skip-verify (intra-cluster, netpol-guarded),
  server-side injection of `Authorization: Basic` (per-workspace
  `VNC_PW` secret, never exposed to the browser).
- Frontend: KasmVNC's web client rendered in an **iframe** (same-origin
  via the wwt proxy).

Ruled out: (B) reinjecting TigerVNC into the kasmweb images (heavy
surgery — KasmVNC *is* the X server —, recurring maintenance per
upstream release, GPL obligations if modified images are
redistributed); oauth2-proxy/ingress per workspace (object churn, RAM
per session, websocket timeouts, coupling to the ingress
implementation); creds in the URL (blocked by browsers).

## Expected impacts by layer

- **CRDs**: `kasmvnc` added to the protocol enums (template + image);
  template `homeMountPath` (default `/home/user`, `/home/kasm-user`
  for Kasm); webhook rejects guacd params on a kasmvnc protocol;
  `/dev/shm` via `workload.volumes` passthrough (Memory emptyDir).
- **Operator**: random `VNC_PW` Secret per workspace, created in the
  pod's ns (credentialsSecretRef mechanism).
- **Governance**: `WorkspaceImage` entries for `docker.io/kasmweb/*`
  pinned by digest (upstream rolling tags → Renovate), `Architectures`
  matching Docker Hub reality. Clipboard ungoverned on this path in v1
  (displayed honestly); phase 4 mitigation = KasmVNC DLP config
  derived from the policy (injectable `-http-header`/VNCOPTIONS).
- **Sessions**: closed when the proxied websocket disconnects (wwt
  stays in the path) + SessionSweeper as a safety net.

## Phase 0 PoC — results (kasmweb/firefox:1.19.0, local)

| Point | Result |
|---|---|
| Web 6901 / TLS / Basic | ✅ `-sslOnly`, realm `Websockify`, auth required before path resolution |
| Iframe-ability | ✅ no `X-Frame-Options` / `frame-ancestors` (COEP/COOP have no iframe effect; same-origin via wwt anyway) |
| Prefixed proxy | ✅ relative assets. ⚠️ the client builds `wss://host/websockify` AT THE ROOT → the iframe will pass `?path=kasm/{id}/websockify` (noVNC mechanism) |
| WS handshake | ✅ 101 + `RFB 003.008` banner. **Trap (read in upstream websocket.c)**: `parse_handshake` requires `Origin:` + `Sec-WebSocket-Protocol` — without `Origin`, the file fallback handler = misleading 404 |
| `VNC_PW` / `VNC_RESOLUTION` | ✅ (resolution → `MaxVideoResolution`; actual geometry driven by client-side dynamic resize) |
| PSA / caps | ✅ non-root uid 1000, runs with `--cap-drop ALL`, Firefox sandbox alive |
| `/dev/shm` | ✅ 512Mi is enough (Firefox) |
| Home persistence | ✅ `HOME=/home/kasm-user`, volume survives recreate, uid 1000 |
| Restart (≈ scale-to-0) | ✅ `KASMVNC_AUTO_RECOVER` — Xvnc + app come back on their own |

**Conclusion: GO.** The wwt proxy needs to set three things toward
upstream: `Authorization: Basic`, `Origin`, `binary` sub-protocol.

## Phased plan

| Phase | Content | Effort |
|---|---|---|
| 0 — PoC | *done, see above* | 1 d |
| 1 — Data plane | wwt reverse-proxy mode, `ConnectionInfo` branch, frontend iframe, session close on WS disconnect | 3-5 d |
| 2 — CRDs/operator | `kasmvnc` enum, `homeMountPath`, `VNC_PW` Secret, webhooks, `test/smoke` smoke test | 2-4 d |
| 3 — Catalog/governance | gitops kasmweb entries pinned by digest, `Architectures`, clipboard display, docs | 1-2 d |
| 4 — Hardening (opt.) | policy-derived DLP kasmvnc.yaml, cert-manager instead of self-signed, kasmvnc params registry | 2-3 d |

## 2026-07-10 update — audit of the kasmvnc API exposed by the proxy

Investigation triggered by a cross-review against
`docs/studies/protocol-feature-matrix-2026-07-10.md`: this document's
"platform only" claims above (line 41, before correction) were checked
against the official KasmVNC docs and two GitHub issues settled by a
project maintainer — two corrections come out of it, plus an
unplanned discovery.

**Audio (playback + mic): confirmed absent from standalone KasmVNC, at
every version.** Issue [kasmtech/KasmVNC#31](https://github.com/kasmtech/KasmVNC/issues/31),
answer from a maintainer (`mmcclaskey`, collaborator): *"No, KasmVNC
does not support audio at this time. Kasm Server does support audio in
and out"* — Kasm Server refers to the commercial product (Kasm
Workspaces), not the standalone server WaaS uses. No mention of audio
in KasmVNC's release changelogs from 2020 through 1.4.0 (Oct 2025,
latest version at the time of the audit): unchanged since the issue
was opened. Nothing to correct in this document — the initial
assumption was right.

**Upload (browser → session): confirmed absent, at every version, and
not planned.** Issue [kasmtech/KasmVNC#209](https://github.com/kasmtech/KasmVNC/issues/209)
(2024), same maintainer: *"File uploads/downloads are not part of VNC
and we don't currently plan on modifying KasmVNC to support it
directly."* No upload API in the current developer docs
(`kasmweb.com/kasmvnc/docs/latest/developer_api.html`).

**Download (session → browser): WRONG in the initial version of this
document.** Standalone KasmVNC exposes a real *Downloads API* since
v0.9.3-beta (2022, "support for downloads over 4GB"), refined through
1.4.0 ("fixed bug with the downloads API not escaping certain
characters in returned json"). The web client embeds a native button
that lists and downloads files from the session's downloads folder.
`wwt/internal/kasm/kasm.go` proxies the entire KasmVNC app with no
path filtering (`proxyTo`, `kasm.go:167-202`): this button was
**already accessible to WaaS users with no policy** — no equivalent of
the `platform` tier that blocks `enable-sftp`/`enable-drive` on guacd
protocols (`operator/pkg/params/params.go:365-371`).

✅ **Verified in a live session (2026-07-10, Feature 11).** On k3d dev,
`GET /kasm/{sid}/api/downloads` with the standard session cookie
returned `200` and the real JSON listing of files in the downloads
folder (metadata: name, size, dates, perms); without a cookie →
`401` (wwt requires the session token, Basic auth is injected
server-side). **Fixed**: `wwt/internal/kasm/kasm.go` now blocks
`/api/downloads` and its subpaths (`403`), parity with guacd's
`TierPlatform` freeze. Verified live after the fix: `403`.

**Unplanned discovery: the exposed kasmvnc API is much broader than a
simple download.** The developer docs
(`developer_api.html`) document nine endpoints, all under
`/api/…`, all "require owner credentials":

| Endpoint | Effect |
|---|---|
| `/api/downloads` | Lists + downloads the session's downloads-folder files |
| `/api/clear_clipboard` | Clears the KasmVNC clipboard AND the session's X clipboard |
| `/api/get_screenshot` | Screenshot of the current session |
| `/api/create_user`, `/api/update_user`, `/api/remove_user` | Manages the session's user accounts (read/write/owner permissions) |
| `/api/send_full_frame` | Forces a full frame resend to all read-only users |
| `/api/get_bottleneck_stats`, `/api/get_frame_stats` | Session telemetry |

The wwt proxy injects the Basic credentials (`VNC_PW` / `kasm_user`)
server-side for **every** request to the pod (`kasm.go:187`) —
including these nine endpoints, since nothing distinguishes them from
asset requests or the `/websockify` WebSocket.

✅ **Live audit (2026-07-10, Feature 11).** `kasm_user` does carry the
`owner` role: the container's `.kasmpasswd` contains
`kasm_user:<hash>:wo` (**w**rite + **o**wner flags). All nine endpoints
respond through the proxy with the standard session cookie:
`downloads` → `200` + real listing, `get_screenshot` → `200` + JPEG of
the desktop (1024×768), `clear_clipboard`/`send_full_frame`/
`get_bottleneck_stats` → `200`, `get_frame_stats` and
`create_user`/`update_user`/`remove_user` → `400` (reached, `owner`
auth accepted, just missing parameters — so fully functional with the
right body). A WaaS user's browser therefore does have full access to
this API.

**Decision (Feature 11)**: only `/api/downloads` is blocked — it's the
only one that overlaps a capability WaaS explicitly governs elsewhere
(file transfer, `TierPlatform` on guacd). The other eight stay proxied
**by documented choice**: a single-user session that administers
itself, with no third party to protect against the user themself (they
already have the keyboard and screen; `get_screenshot` only leaks their
own desktop to their own browser; `create_user`/`remove_user` only
create accounts on a pod for which wwt remains the sole entry point,
injecting `kasm_user` regardless). Reflexively blocking those eight
would be defending against the user's own capabilities on their own
session.

**Multi-monitor: confirmed standalone**, not a platform exclusive — the
client docs (`clientside.html`) document a native "Display Manager"
button to add/position additional screens, with no mention of a Kasm
Workspaces dependency. Consistent with what this document already said
(line 41); simply never verified in a live WaaS session (same ❓
status as in the protocol × feature matrix, note 11).

**Consequence — shipped (Feature 11, 2026-07-10).** The broadened
follow-up (clipboard *and* API scope) is done:

- **`/api/downloads` blocked** at the wwt proxy (403), see above.
- **Clipboard genuinely governed.** The operator derives KasmVNC DLP
  directives from `WorkspacePolicy.Clipboard` and merges them into
  `kasmvnc.yaml` (`operator/internal/controller/kasm_config.go`):
  `data_loss_prevention.clipboard.server_to_client.enabled` (copy),
  `…client_to_server.enabled` (paste), and
  `runtime_configuration.allow_client_to_override_kasm_server_settings:
  false` (without which the client can re-enable the clipboard — the
  official docs are explicit about this). The opaque admin config
  (`kasmvncConfig`) is preserved for everything else; only the
  clipboard block is authoritative. Denying the clipboard genuinely
  disables copy/paste inside the container (verified live: policy deny
  → `enabled: false` on both sides). Enforcement follows the
  workspace's **owner** (container DLP is one-per-workload; on personal
  kasmvnc namespaces `waas-{user}`, owner == user).
- **Template-side safeguard.** Since the operator overwrites these
  three keys from the policy, an admin who wrote them by hand into
  `kasmvncConfig` would be silently ignored. The validating webhook
  (`workspacetemplate_webhook.go`) therefore rejects a template whose
  `kasmvncConfig` sets one of the managed keys, pointing to
  `WorkspacePolicy.Clipboard` (same "honest refusal beats a silently
  ignored field" principle as the rest of the webhook). The unmanaged
  sub-keys (`size`, `allow_mimetypes`, `delay_between_operations`,
  `primary_clipboard_enabled`) remain the admin's. Verified live.
- **Faithful UI.** `SessionOverlay.tsx` reads `capabilities.clipboardCopy/
  Paste` (applied state, read-only) instead of a static text, with the
  note "native KasmVNC clipboard, enforced inside the container".

Original prompt: `docs/studies/08-prompt-feature11-kasmvnc-governance-gap.md`
(replaces the obsolete Feature 4).

## 2026-07-10 update (continued) — KasmVNC default and admin editor

Two gaps left by Feature 11, fixed afterward.

**KasmVNC config hierarchy — verified in a live session.** KasmVNC
itself merges three files, from lowest to highest priority, **key by
key** (deep-merge):

1. `/usr/share/kasmvnc/kasmvnc_defaults.yaml` — the full defaults
   shipped by the kasmweb/\* image (1024×768 resolution, encoding, SSL,
   CORS headers, brute-force, default
   `data_loss_prevention.clipboard` block…). Official directive docs:
   <https://kasmweb.com/kasmvnc/docs/latest/configuration.html>.
2. `/etc/kasmvnc/kasmvnc.yaml` — system override (small; SSL pem
   paths, `runtime_configuration`).
3. `~/.vnc/kasmvnc.yaml` — user override, **this is where WaaS mounts
   its ConfigMap** (read-only, subPath).

Live proof: the mounted user file only contained
`data_loss_prevention` + `runtime_configuration`, but the actual
`Xvnc` process carried `-geometry 1024x768`, `-MaxVideoResolution
1920x1080`, `-BlacklistThreshold 5`, `-http-header
Cross-Origin-Embedder-Policy=require-corp` — all coming from
`kasmvnc_defaults.yaml`, absent from the other two files. **So the
image's defaults already apply** even when the WaaS file is partial:
WaaS doesn't need to re-supply a default layer.

**Consequence**: we do NOT introduce a frozen default constant on the
WaaS side. Copying `kasmvnc_defaults.yaml` into the code would freeze
these defaults against any future image version (across all images at
once) — exactly the anti-pattern we avoid for CRD defaults. WaaS's
merge therefore stays at **two layers** (admin config → clipboard
policy keys), and the effective "base" is the image's file, not ours.
The CRD field comment (`workspacetemplate_types.go`) and the package
comment (`kasm_config.go`) have been corrected accordingly (the old
"Empty = no mount, the image default applies" was wrong about the
*mount*, which is now unconditional for a kasmvnc template).

**Admin editor.** `kasmvncConfig` now has a `<textarea>` in
`TemplatesPage.tsx` (`admin/`), shown only when a `kasmvnc` protocol is
present (same guard as the webhook), with help text explaining the
propagation (merge over image defaults, read-only mount, clipboard
keys rejected here) and a link to the official docs above. The backend
plumbing (`TemplateInput` → `WorkspaceTemplate` → `types.gen.ts`) was
already complete; only the `toInput` round-trip and the frontend facade
types were missing. No preview of the final merge in the UI (the
textarea edits the raw override layer) — not requested, would avoid
duplicating the merge on the client side.
