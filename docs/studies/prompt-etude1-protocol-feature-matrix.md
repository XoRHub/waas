# Fable 5 Prompt — Study 1: table of features supported per protocol

Paste this document as-is as a study prompt (**no implementation expected**, only a markdown deliverable). It assumes that you (Fable 5) have no prior conversation context.

## Goal

Produce a sourced state-of-the-art (file + line, as `docs/studies/audit-2026-07.md` already does — reread it for the tone and rigor level expected — no estimated figures, everything verified in the code): **a protocol × feature cross-reference table**, saying for each cell whether it's supported, partially supported, or not supported, with the exact source for the claim. The target format is close to `docs/templates-and-protocols.md` (already existing, read it first — don't restate what it already documents, reference it) but organized as a table of cross-cutting features rather than a narrative doc by topic.

## Protocols to cover

Four connection paths exist today, to be treated as four distinct columns (they don't share the same mechanisms under the hood):
- **VNC** — brokered by guacd.
- **RDP** — brokered by guacd.
- **SSH** — brokered by guacd (terminal, not a graphical desktop — some table rows don't apply, mark them explicitly "N/A" rather than "not supported").
- **KasmVNC** — a **different** path: no guacd, raw HTTP reverse-proxy done by `wwt/internal/kasm` directly to the KasmVNC server embedded in the image (`docs/studies/kasm-images-feasibility.md` for the decision's context, "Kasm phase 1-4" section of the project journal if accessible). guacd parameters don't exist on this path — check this in `operator/pkg/params/params.go` (`Protocols()` at the end of the file: `kasmvnc` is listed but **no registry entry has `kasmvnc` in `Protocols`** → any override there is rejected fail-closed).

Also treat separately, as a note or side column if relevant, **remote workspaces** (`RemoteWorkspace`, `model.RemoteProtocol`) — these are out-of-cluster machines connected via the same wwt but with a different policy model (no Workspace template/CRD, lighter governance); some features in the table may differ between an in-cluster workspace and a remote machine on the same protocol.

## Features to evaluate (starting base — complete it if you find others while reading the code)

For each one, the primary source of truth is `operator/pkg/params/params.go` (the registry — every `Param` has `Protocols`, `Tier`, `Live`, `Default`) and `docs/guacd-parameters.md` (generated from the registry by `make docs-params`, `operator/cmd/paramsdoc/main.go` — check it's up to date, regenerate it if needed rather than reading a stale file). Don't just copy the registry: say for each feature whether it's **exposed in the UI** (`Tier: ui`), **accessible only via CR/YAML** (`Tier: advanced`), or **entirely blocked platform-side** (`Tier: platform`, with the reason written in `Description`), and whether it's **applicable live without reconnecting** (`Live: true`).

1. **Sound / audio** — playback (`enable-audio` VNC, `disable-audio`+active default RDP) and mic/input (`enable-audio-input` RDP). SSH = N/A. KasmVNC = to be checked in the feasibility doc (`docs/studies/kasm-images-feasibility.md` mentions "audio = Kasm platform only, not our standalone path" — confirm whether this is still the current state).
2. **Copy/paste (clipboard)** — `disable-copy`/`disable-paste` (VNC/RDP/SSH, `Live: true`, enforced by wwt's `ClipboardFilter`). Read `docs/clipboard.md` in full, it already has a partial matrix (RDP limited by the xrdp-libvnc image, Firefox without `readText`) — check it's still accurate and incorporate it. KasmVNC: the clipboard is **not governed** on this path (mentioned as an accepted v1 decision in the project journal) — check the current state in `wwt/internal/kasm` (is there an equivalent `ClipboardFilter`, or really nothing?).
3. **Shared volume / persistent home** — the home PVC is always mounted (`docs/volumes.md`), a PVC-label-based retention model (no DB table). Check whether a real "volume shared across multiple workspaces" exists (probably not — each workspace has ITS OWN home; if there's only an adoption mechanism for an existing volume at creation time, say so clearly, that's not the same thing as concurrent sharing).
4. **File transfer** — `enable-sftp` (VNC/RDP/SSH) and `enable-drive` (RDP) are `Tier: platform`, explicitly blocked with the reason "until the file-transfer feature has its own policy gate" — **not supported today**, say so unambiguously rather than listing the parameter as "available in advanced".
5. **Session recording** — `recording-path`/`recording-name`/`create-recording-path`/`typescript-path`: all `Tier: platform`, same status as point 4 (not supported, pending a dedicated policy gate).
6. **Keyboard layout** — `server-layout` (RDP, `Tier: ui`, enum of ~24 layouts + auto-detection from the browser locale, `docs/templates-and-protocols.md` keyboard section). VNC/SSH: check whether an equivalent exists (a priori no — VNC keyboard follows the X server's layout, no guacd negotiation).
7. **Resizing / resize** — `resize-method` (RDP, `Tier: ui`, live-update vs reconnect). VNC: check whether an equivalent parameter exists in the registry (a priori VNC resolution is fixed server-side on Xvnc, no dynamic resize via guacd). KasmVNC: `docs/studies/kasm-images-feasibility.md` mentions a native client-side dynamic resize (`AcceptSetDesktopSize`) — confirm whether the current wwt path already uses it.
8. **Multi-monitor** — mentioned as a KasmVNC standalone capability in the feasibility study; check whether `frontend/src/components/DesktopPane.tsx` or the KasmVNC iframe (`wwt/internal/kasm`) actually expose it today, or whether it stayed theoretical.
9. **Image quality / color** — `color-depth` (VNC+RDP), `force-lossless`/`swap-red-blue` (VNC), local/remote `cursor` (VNC).
10. **Parameters applicable live (`Live: true`)** — make this a separate column/list: as of now, only `disable-copy`/`disable-paste` are in the registry (`grep Live: true` in `params.go`); check that none other has been added since.
11. **Workspace-level overrides after creation** — if you're carrying out this study after Feature 1 (`docs/studies/prompt-feature1-workspace-runtime-config.md`) has been implemented, mention its existence and status; otherwise explicitly note that this is **not yet possible today** (only the initial creation lets you set env/resources/placement).

## Expected format

A single markdown file, in the style of `docs/studies/audit-2026-07.md`:
- A main `Feature × {VNC, RDP, SSH, KasmVNC}` table with short values (`✅ ui`, `⚙️ advanced (CR/YAML only)`, `🚫 platform-blocked`, `N/A`, `❓ to verify live`) and a footnote per row pointing to the source (file:line).
- A slightly longer section per feature when the topic has a backstory (clipboard, intentionally blocked file-transfer/recording, kasmvnc outside the registry) — this is where `docs/clipboard.md`, `docs/volumes.md`, `docs/templates-and-protocols.md`, and the kasm study must be cited and reconciled, not just linked.
- A final "Gaps vs. what the UI implies" section if you find places where the frontend displays something (e.g. a parameter in a form) that in reality does nothing for a given protocol, or the reverse (a real capability not exposed in the UI).

## Constraints

- **No implementation.** This document is an audit, not a dev ticket — don't add or modify code.
- Every claim must be sourced (file + line, or the name of an existing doc) — no estimates, no "probably". If you can't verify a point with certainty (e.g. runtime behavior untestable without a cluster), say so explicitly rather than guessing.
- Regenerate `docs/guacd-parameters.md` if you suspect it has drifted from the registry (`make docs-params`) before relying on it.
- Drop the deliverable in `docs/studies/` (suggested name: `docs/studies/protocol-feature-matrix-<date>.md`) — don't commit it yourself unless explicitly asked to.
