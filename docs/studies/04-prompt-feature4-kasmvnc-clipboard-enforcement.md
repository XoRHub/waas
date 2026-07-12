# Fable 5 Prompt — Feature 4: make the clipboard work (and honestly enforce it) on kasmvnc sessions

> **Obsolete, superseded by `08-prompt-feature11-kasmvnc-governance-gap.md`
> (2026-07-10).** Only the display part (§C below) was
> delivered (commit `87464e8a7865`); the actual enforcement (§A/§B) was
> never delivered and was absorbed, with an expanded scope (kasmvnc API
> exposed without filtering), into Feature 11. Do not resume this
> document — use the new one.

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

WaaS drives VNC/RDP/SSH sessions via guacd, and kasmvnc via a separate path: `wwt/internal/kasm` does a raw HTTP/WebSocket reverse proxy to the KasmVNC pod (`kasm.go:167-202`, `httputil.ReverseProxy`), **never going through guacd at all**. `wwt/internal/proxy/proxy.go:96-102` even explicitly refuses kasmvnc on `/ws` ("kasmvnc sessions connect through /kasm, not /ws"). guacd's `ClipboardFilter` (`guac.NewClipboardFilter`, instantiated only in the guacd pipe, `proxy.go:142`) therefore has **no hold** on kasmvnc — this is structural, not an oversight.

## What already exists (know this before coding)

**The clipboard grant is protocol-agnostic today, everywhere in the chain:**

- CRD: `WorkspacePolicySpec.Clipboard` (`operator/api/v1alpha1/workspacepolicy_types.go:63-68`) → `ClipboardPolicy{CopyFromWorkspace, PasteToWorkspace *bool}` (lines 97-108). No protocol field.
- Resolution: `policy.ClipboardOf(pol)` (`operator/pkg/policy/policy.go:161-174`) returns `(true,true)` if `pol.Spec.Clipboard` is nil, otherwise both bools — takes no protocol parameter.
- Consumption: `resolveClipboardGrant` (`api-server/internal/service/workspace_service.go:629-646`), called from `workspace_service.go:607` and `remote_workspace_service.go:437` — **neither call branches on the session's protocol**.
- Exposure to the frontend: `SessionCapabilities{ClipboardCopy, ClipboardPaste bool}` (`api-server/internal/model/model.go:466-469`), populated identically for all protocols (`workspace_service.go:597-598`, `remote_workspace_service.go:450-451`).

**On the guacd registry side**, `disable-copy`/`disable-paste` (`operator/pkg/params/params.go:83-92`) are scoped `Protocols: []string{"vnc","rdp","ssh"}` — **kasmvnc is explicitly excluded**, even though `Protocols()` (line 447) does list `vnc, rdp, ssh, kasmvnc` as a recognized protocol. Consistent with the fact that these params only make sense within the guacd tunnel.

**Commit `87464e8a7865` (already delivered)** made the UI honest on the display side only: `frontend/src/components/SessionOverlay.tsx` now shows, for `protocol === 'kasmvnc'`, a static explanatory sentence (`overlay.clipboardKasmvnc`) instead of the live copy/paste toggles — because `tunnelRef` is `null` and `sendClipboardRef` a no-op on this path. The commit message explicitly states: the backend still emits `capabilities.clipboardCopy/Paste` regardless of protocol — **this was a display-only fix, no enforcement change.**

**KasmVNC has its own clipboard mechanism**, entirely outside this codebase: `docs/studies/kasm-images-feasibility.md:41` notes that the KasmVNC web client (standalone mode) has a built-in browser-native clipboard, independent of guacd. The documented v1 decision (same file, line 5 and 84) is "ungoverned clipboard acceptable in v1 with honest display" — this feature lifts that limitation. There is **no local KasmVNC configuration in this repo**: the `kasmweb/*` images are pulled as-is from Docker Hub (no Dockerfile/`kasmvnc.yaml`/supervisord in `waas-images/` for kasmvnc) — so there is no existing hook to drive KasmVNC's native clipboard from WaaS.

## What needs to be delivered

### A. Make the grant honest end-to-end (mandatory, independent of the rest)

`resolveClipboardGrant` must become protocol-aware: for kasmvnc, as long as nothing actually enforces the grant (before B is delivered), it must **not** report `clipboardCopy`/`clipboardPaste` as a right granted by the WaaS policy — either by forcing it to `false`, or by adding an explicit "ungoverned" state distinct from `true`/`false`, surfaced in `SessionCapabilities` so the frontend can display a truly faithful state (not just a static text disconnected from what `capabilities` says).

### B. Make real enforcement work for kasmvnc

**This requires research into KasmVNC itself, which is not documented in this repo — don't make it up, check the real version/docs of the `kasmweb/*` image in use.** KasmVNC (upstream, Kasm Technologies project) generally exposes clipboard control via:
- environment variables at container startup (to verify on the image actually used — look for `KASM_` or equivalent in the image's logs/docs, or in the `kasmvncserver` binary/`kasmvnc.yaml` if extracted from the container), and/or
- KasmVNC web client URL parameters (query string on the served page), and/or
- a `kasmvnc.yaml` config file mounted in the container.

Investigate which of these paths is actually available on the image in use (local `docker run` + inspection, or `docker exec` + reading the config files present), then wire it up: when `policy.ClipboardOf` returns `(false, false)` for a kasmvnc workspace, the container's effective configuration (via an env var injected by the operator at provisioning time, or via a generated config file) must actually disable copy/paste on the KasmVNC client — not just hide a button on the WaaS side.

### C. Faithful UI

Once B is in place, `SessionOverlay.tsx` must reflect the real state (allowed/not allowed) rather than the current static sentence — replace the fixed text from 87464e8a7865 with a display that reads `capabilities.clipboardCopy`/`clipboardPaste` like other protocols, possibly with a "native KasmVNC clipboard" note to distinguish it from the guacd mechanism if useful to the user.

## Constraints to respect

- Don't touch the guacd path (`disable-copy`/`disable-paste`, `ClipboardFilter`) — this feature is strictly the kasmvnc path.
- If the KasmVNC enforcement mechanism requires a change to pod provisioning (env var, mounted ConfigMap), go through the operator (`operator/internal/controller/workload.go` / `kasm_credentials.go`) the same way as other existing kasm config injections — no ad-hoc logic on the `wwt` side.
- Tests: Go coverage on the new protocol branch of `resolveClipboardGrant` (kasmvnc vs others), and on the operator wiring if you add an env var/ConfigMap. Vitest test on the new `SessionOverlay.tsx` render for kasmvnc (at least: true grant → toggles/info shown, false grant → honest message).
- i18n: any new string goes through `frontend/src/i18n/locales/{en,fr}.json`.
- Update `docs/studies/kasm-images-feasibility.md`: the "ungoverned clipboard acceptable in v1" decision becomes obsolete once this feature ships — document the new mechanism instead, don't let the two versions coexist in the same file.

## Open points (your call)

- Exact enforcement mechanism on the KasmVNC side (env var / config file / query param) — depends on the image actually used, to be determined by investigation before coding, not by assumption.
- Representation of "ungoverned" in transition (before B is delivered, if you ship A and B in separate changes) — a three-value state (`granted`/`denied`/`ungoverned`) is more honest than a plain bool, but changes the `SessionCapabilities` contract; document the choice if you introduce this triple state.
- If the KasmVNC image in use offers no programmatic clipboard control at all (some consumer-grade images don't expose it), document this clearly as an upstream technical limitation rather than forcing partial enforcement that would give a false impression of security.
