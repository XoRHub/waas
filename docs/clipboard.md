# Clipboard: full chain, precedence, and expected matrix

## Two disjoint mechanisms, one authority

The clipboard is not governed by ONE resolution path but by TWO, with
no shared code beyond `WorkspacePolicy.spec.clipboard` and
`policy.ClipboardOf()`:

- **guacd (`vnc`/`rdp`/`ssh`)**: 4 layers (policy, template `params`,
  template `userParams`, connection override), resolved on EVERY
  connection, enforced by the wwt proxy via the connection token.
- **`kasmvnc`**: a single layer (policy), resolved at RECONCILE time
  (not at connection time), baked into `~/.vnc/kasmvnc.yaml` by the
  operator (see `docs/kasmvnc.md`). Template `params`/`userParams` have
  no effect at all: `disable-copy`/`disable-paste` are only registered
  for `vnc`/`rdp`/`ssh` (`operator/pkg/params/params.go`), and the
  template webhook rejects a `userParams` citing these names on a
  `kasmvnc` entry — which is anyway impossible to combine with
  `vnc`/`rdp`/`ssh` on the same template (kasmvnc is exclusive). The
  inconsistent configuration is prevented at admission, not silently
  ignored.

In both families the policy is the sole security authority: the
template can never relax it — on the guacd side it can only restrict
further (logical AND, never OR), on the kasmvnc side it has no lever at
all. There is one ceiling and cascading restrictions, not three
independent decision points.

### Configuration points

| Field | Who edits it | Protocol scope | When evaluated | Real role |
|---|---|---|---|---|
| `WorkspacePolicy.spec.clipboard` (`copyFromWorkspace`/`pasteToWorkspace`) | admin, CR | all | every connection (guacd) / every reconcile (kasmvnc) | **security ceiling, sole authority for kasmvnc** |
| `WorkspaceTemplate.spec.protocols[].params["disable-copy"/"disable-paste"]` | admin, template CR | `vnc`/`rdp`/`ssh` only | every connection, template refetched | default value applied if the user doesn't submit an override |
| `WorkspaceTemplate.spec.protocols[].userParams` | admin, template CR | `vnc`/`rdp`/`ssh` only | every connection | **delegation of NAMES**, not values: which parameters the user may submit as an override |
| `ConnectInput.Params` (connection-settings dialog) | connecting user (or template owner / admin) | `vnc`/`rdp`/`ssh` only, delegated names only | every connection, ephemeral — never persisted on a CR | effective value requested for THIS session |
| Session menu (`SessionOverlay.tsx`) | read-only | all | display only | mirrors the already-clamped result + names WHO blocked it (`ClipboardLockPolicy` vs `ClipboardLockParams`) |

The session menu is never a decision point — it mirrors the result
already computed server-side (`clipboardCapabilities`,
`api-server/internal/service/workspace_service.go`).

### Resolution chain — guacd (`vnc`/`rdp`/`ssh`)

Code: `WorkspaceService.Connect` and
`clampClipboardGrant`/`mergeParams`/`clipboardCapabilities`
(`api-server/internal/service/workspace_service.go`).

1. `policyGrant = policy.ClipboardOf(resolved policy of the CONNECTING
   user)` — resolution failure (no user, no matched policy) fails
   closed, `(false, false)`.
2. For each direction (copy / paste), the effective param value is: the
   user's `ConnectInput.Params` value if the name is delegated
   (`userParams`, or actor is admin / declared template owner) AND the
   policy allows the `protocolParams` override field; otherwise the
   template's `params` value if present; otherwise absent.
3. `effectiveGrant.direction = policyGrant.direction AND NOT(effective
   value == true)` — a param can only restrict, never widen beyond the
   policy (guarded by `connect_clipboard_test.go`).
4. The signed connection token embeds `effectiveGrant` — that is what
   wwt enforces via the tunnel. `clipboardCapabilities` builds only the
   display view plus the blocking-reason label (policy wins the label
   if both block).

Note that `params` (locked) and `userParams` (delegated) are **not
mutually exclusive**: a name may appear in both (`params` supplies a
default, `userParams` lets the user change it for their session). An
admin who wants a real lock simply omits the name from `userParams` —
putting it in `params` alone fixes the value for everyone.

### Resolution chain — `kasmvnc`

Code: `ensureKasmConfig`/`kasmClipboardGrant`/`applyClipboardPolicy`
(`operator/internal/controller/kasm_config.go`).

1. At reconcile (not at connection time): `policy.ClipboardOf(resolved
   policy of the workspace OWNER, not of the connecting user)` —
   container-level DLP can only enforce one policy per workload, so it
   follows the owner. Resolution failure fails closed.
2. The two booleans are stamped into the effective `kasmvnc.yaml`
   (admin's `kasmvncConfig` + these keys last, so authoritative):
   `data_loss_prevention.clipboard.server_to_client.enabled` (copy),
   `…client_to_server.enabled` (paste), and
   `runtime_configuration.allow_client_to_override_kasm_server_settings:
   false` (always — otherwise the KasmVNC client can reopen what the
   server closed).
3. Template `params`/`userParams` never come into play — no
   delegation lever exists for kasmvnc, only the policy. An admin can
   therefore never delegate the clipboard to the user on a kasmvnc
   workspace, even partially — an accepted structural asymmetry
   (kasmvnc has no guacd tunnel to instrument).
4. Separately, at connection time, the api-server computes
   `capabilities` for display — with the CONNECTING user's policy, not
   the owner's.

**Watch point (shared kasmvnc workspaces)**: the policy actually
enforced inside the container is the **owner's**, whereas a guest's
session menu reflects THEIR OWN policy. On a share between users with
different policies the menu can diverge from what the container
enforces. Not an exploitation path — the container DLP is never more
permissive than the owner's policy; the annoying case is UX-only (menu
more optimistic than reality). To revisit if RW/RO sharing of kasmvnc
workspaces becomes a real usage pattern.

## Chain (and where each link applies)

```
WorkspacePolicy.spec.clipboard ──► connection token (grant, signed)
        │                                   │
        ▼                                   ▼
/connect capabilities               wwt ClipboardFilter (ENFORCEMENT:
(the overlay DISPLAYS, never        drops "clipboard" streams +
 enforces)                          live toggles clamped to the grant)
                                            │
        browser ◄── guac stream ──► guacd ◄──► desktop (VNC/RDP/SSH)
            │
   DesktopPane (client integration):
   onclipboard → local clipboard ; paste/focus → createClipboardStream
```

- **Policy**: `clipboard.copyFromWorkspace` / `pasteToWorkspace` of the
  resolved policy. Fail-closed: no resolved policy = no clipboard.
  The grant travels in the connection JWT — wwt enforces, the UI reflects.
- **guacd**: no connection parameter to pass — clipboard is
  part of the guac protocol; there is **no** restrictive default
  on the guacd side that would turn it off (`disable-copy`/`disable-paste`
  are banned from the platform-side registry, enforcement lives in wwt).
- **wwt**: `ClipboardFilter` drops streams in the refused direction
  (+ error ack 771 on the paste side) and handles the overlay's
  live `waas-clipboard` toggles, clamped to the grant.
- **Web client** (the missing link — the cause of "nothing works on
  any protocol"): `DesktopPane` now relays both directions:
  - desktop → local: `client.onclipboard` → `navigator.clipboard.writeText`
    (best-effort) + a buffer exposed to the overlay (manual exchange);
  - local → desktop: re-reading the system clipboard on window focus
    (Chromium + HTTPS), with an anti-echo guard (`lib/clipboard.ts`,
    tested). The DOM `paste` event stays wired but is only a
    theoretical safety net: a real Ctrl+V in the pane never fires it —
    `Guacamole.Keyboard` calls `preventDefault()` on the relayed keydown,
    which suppresses the native paste action (verified live,
    2026-07). Without focus-sync, the real path is the overlay's
    manual exchange.

## Secure context: what the browser allows

| Context | copy (desktop→local) | paste (local→desktop) |
|---|---|---|
| HTTPS + Chromium | automatic (`writeText`) | automatic on focus (`readText`, permission requested) |
| HTTPS + Firefox | automatic (`writeText`) | manual exchange via the overlay (no `readText`, and Ctrl+V does not trigger the `paste` event in the pane) |
| HTTP | **manual exchange via the overlay only** | **manual exchange via the overlay only** |

The dev env serves both: `https://waas.127.0.0.1.nip.io:8443`
(self-signed cert, seamless OK) and `http://…:8080` (smoke tests; no
secure context, so seamless off). Verified end-to-end in real
Chromium on the dev k3d on 2026-07-10 — protocol and results in
`docs/studies/16-report-clipboard-https-dev-verification.md`.

The overlay (Ctrl+Alt+M → Clipboard → Manual exchange) shows the last
text received from the desktop and lets you send one — this is the
verification path independent of browser permissions.

## Expected matrix {protocol × direction × policy}

Enforcement (wwt) is protocol-independent: the policy table holds
for VNC, RDP and SSH alike.

| Direction | Policy ✔ | Policy ✘ |
|---|---|---|
| Copy from the workspace | text copied on the desktop arrives (auto or overlay) | stream dropped by wwt; toggle greyed out 🔒 |
| Paste to the workspace | focus-sync pushes the text, the desktop app pastes it | stream refused (ack 771); toggle greyed out 🔒 |
| Overlay toggle OFF then ON | turns off then restores live (≤ grant) | stays OFF: the wwt response reflects the effective state |

Reality per protocol (desktop side, `waas-images` images):

- **VNC**: recommended path — Xvnc handles the cut-buffer natively,
  both directions work.
- **RDP**: works, **text only** — the xrdp-libvnc backend embeds
  its own cliprdr ↔ cut-text RFB bridge (`vnc/vnc_clip.c`), without
  chansrv. Verified in a real session against guacd 1.5.5 in both
  directions (2026-07). Non-text formats (files, images) don't go
  through; the wwt filter applies identically.
- **SSH**: the terminal is rendered by guacd, which has its own
  terminal clipboard — both directions go through the same guac
  streams, same rules.

## Tests

- wwt: `wwt/internal/guac/clipboard_test.go` — both directions ×
  grant × live toggles (clamp), acks of refused streams.
- frontend: `src/lib/clipboard.test.ts` — dedup, anti-echo guard,
  manual-fallback buffer.
- session verification: overlay → Manual exchange (independent of
  browser permissions), on one session per protocol after
  `make smoke`.
