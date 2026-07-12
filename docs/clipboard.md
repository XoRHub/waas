# Clipboard: full chain and expected matrix

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
