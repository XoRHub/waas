# Fable 5 Prompt — Feature 11: close the kasmvnc governance gap (exposed API + clipboard)

Paste this document as-is as an implementation prompt. It assumes you
(Fable 5) have no prior conversation context. **Priority: this is the
next kasmvnc feature to implement**, before any new functionality on
this protocol — it replaces and absorbs
`docs/studies/04-prompt-feature4-kasmvnc-clipboard-enforcement.md`,
which was left unfinished (only a display fix was delivered, commit
`87464e8a7865` — no real enforcement).

## Repo context

WaaS drives KasmVNC via a path separate from guacd:
`wwt/internal/kasm/kasm.go` does a raw HTTP/WebSocket reverse proxy
to the KasmVNC pod (`proxyTo`, `kasm.go:167-202`), with no path
filtering — every request under `/kasm/{sid}/…` is relayed as-is,
with Basic Auth credentials injected server-side (`kasm.go:187`,
`pr.Out.SetBasicAuth(username, info.Password)`, `username` defaulting
to `"kasm_user"`).

**What this prompt fixes**: external research (official KasmVNC docs
+ two GitHub issues settled by an upstream maintainer) has updated
two things documented in
`docs/studies/kasm-images-feasibility.md` §"2026-07-10 update"
— read that section before starting, it contains the exact sources:

1. KasmVNC standalone exposes an **HTTP API `/api/…` with nine
   endpoints** (listed and detailed in the section cited above),
   all documented as "require owner credentials". The wwt proxy does
   not distinguish these endpoints from the rest of the traffic: if
   `kasm_user` holds the `owner` role on its own session (to confirm,
   see task A), any WaaS user has access to it today without any code
   in the repo ever having decided to expose it.
2. Of these nine, only one overlaps with a capability WaaS explicitly
   governs elsewhere: `/api/downloads` (session file download →
   browser). On guacd protocols, the equivalent (`enable-sftp`,
   `enable-drive`) is blocked `TierPlatform` "until the file-transfer
   feature ships with its own policy gate"
   (`operator/pkg/params/params.go:365-371`). Nothing equivalent
   exists on the kasmvnc side — not by documented choice, but a blind
   spot.
3. The kasmvnc clipboard remains, as documented by the original
   Feature 4, **honestly displayed but still not governed**:
   `resolveClipboardGrant` (`api-server/internal/service/workspace_service.go:608-647`)
   still takes no protocol parameter — verified as of this prompt.
   `SessionOverlay.tsx:251-256` shows a static text for
   `protocol === 'kasmvnc'` instead of reading `capabilities`.

## What already exists (know this before coding)

- **Protocol-agnostic clipboard grant everywhere in the chain**:
  CRD `WorkspacePolicySpec.Clipboard` with no protocol field
  (`operator/api/v1alpha1/workspacepolicy_types.go:63-68`),
  `policy.ClipboardOf` with no protocol parameter
  (`operator/pkg/policy/policy.go:161-174`), consumed identically by
  `workspace_service.go:607` and `remote_workspace_service.go:437`
  (kasmvnc is banned from remote workspaces anyway since
  `5e1e737d9a00`, so only the first call matters here).
- **`kasmvncConfig`** (`WorkspaceTemplate.spec.kasmvncConfig`) is
  today the only KasmVNC configuration channel: an opaque YAML
  string, never parsed by the repo, materialized into a ConfigMap and
  mounted at `<homeMountPath>/.vnc/kasmvnc.yaml`
  (`operator/internal/controller/kasm_config.go`). An admin can
  already write KasmVNC DLP directives (clipboard, etc.) into it by
  hand — nothing derives them automatically from
  `WorkspacePolicySpec.Clipboard` today.
- **No existing hook** to dynamically drive the clipboard or the
  kasmvnc API from the operator: unlike other injections (`VNC_PW`
  Secret, `kasm_credentials.go`), there is no mechanism that
  recomputes a kasmvnc config from the policy on reconcile.
- **Existing tests**: no test covers the protocol-specific behavior
  of `resolveClipboardGrant`, nor the exposure (or not) of the
  `/api/…` endpoints by the wwt proxy.

## What needs to be delivered

Handle in this order — B and C depend on the choice made in A.

### A. Audit and decide on kasmvnc API exposure (mandatory, first)

1. In a live session (k3d dev, `make` — check
   `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md` for
   bootstrap if needed), concretely confirm: which role `kasm_user`
   holds on its own session (owner? other?), and which of the nine
   `/api/…` endpoints actually respond through the wwt proxy
   (`/kasm/{sid}/api/downloads`, etc.) with the standard session
   cookie. Don't assume anything — the inventory in
   `kasm-images-feasibility.md` comes from upstream developer docs,
   not from a test against the image actually used by WaaS.
2. Based on this, decide: should `wwt/internal/kasm/kasm.go` continue
   proxying everything unfiltered, or introduce a path allowlist/
   denylist for the eight endpoints with no governance equivalent
   elsewhere (`get_screenshot`, `create_user`/`update_user`/
   `remove_user`, `send_full_frame`, `get_bottleneck_stats`,
   `get_frame_stats`, `clear_clipboard`)? For a single-user session
   that administers itself, most are probably inconsequential (the
   user already has the keyboard and the screen) — document this
   reasoning rather than blocking reflexively. `/api/downloads` is the
   only one with mandatory handling, see B.

### B. Govern `/api/downloads` like the rest of file transfer

1. Decide and implement one of two treatments, consistent with the
   reasoning already established for `enable-sftp`/`enable-drive`
   (`params.go:365-371`, "until the file-transfer feature ships with
   its own policy gate"):
   - either **block** `/api/downloads` at the wwt proxy level as long
     as no file-transfer policy exists (consistency with guacd: no
     file leak via any path, no exception for kasmvnc);
   - or **condition** it on an explicit grant if you identify that a
     transfer policy already exists or is in progress elsewhere in
     the repo (verify before choosing this option — as of this
     prompt, `TierPlatform` is an unconditional block, not a
     configurable gate).
2. Implement the choice in `wwt/internal/kasm/kasm.go` (it's the only
   place that sees every proxied request) — stay consistent with the
   file's style (no new dependency if avoidable, explicit rather than
   silent error handling).
3. Go test: a request to `/kasm/{sid}/api/downloads` must be rejected
   (or granted, depending on your choice) deterministically and
   tested, not just documented.

### C. Finish the clipboard (resuming the original Feature 4)

1. **Honest grant**: make `resolveClipboardGrant` protocol-aware for
   kasmvnc — either it forces `false`/`false` as long as real
   enforcement doesn't exist, or you introduce an explicit state
   distinct from `true`/`false` (`ungoverned`) surfaced in
   `SessionCapabilities` (`api-server/internal/model/model.go`) so the
   frontend displays a faithful state rather than a static text
   disconnected from `capabilities`.
2. **Real enforcement**: investigate KasmVNC's DLP configuration
   (official docs `kasmweb.com/kasmvnc/docs/latest/configuration.html`
   — don't make it up, the directive names haven't been verified in
   this study) to determine how to disable the clipboard from
   `kasmvnc.yaml` or via the `/api/clear_clipboard` API (which, unlike
   the others, IS inventoried — check whether it can serve as an
   ongoing constraint mechanism rather than a one-off action). Wire
   it up: when `policy.ClipboardOf` returns `(false, false)` for a
   kasmvnc workspace, the container's effective configuration must
   actually disable copy/paste — not just hide a button. Go through
   the operator (`kasm_config.go`, the same mechanism as
   `ensureKasmConfig`), not ad-hoc logic on the wwt side.
3. **Faithful UI**: replace the static text in
   `SessionOverlay.tsx:251-256` with a render that reads
   `capabilities.clipboardCopy`/`clipboardPaste` like other protocols,
   with a "native KasmVNC clipboard" note if useful to distinguish it
   from the guacd mechanism.

### D. Documentation

- Once A/B/C are decided and delivered, update
  `kasm-images-feasibility.md` (the ❓ marks in the "2026-07-10
  update" section must become verified facts) and
  `protocol-feature-matrix-2026-07-10.md` (note 7, added to point
  here — replace it with the final state once this prompt is
  complete).

## Constraints to respect

- Don't touch the guacd path (`ClipboardFilter`, `disable-copy`/
  `disable-paste`) — this prompt is strictly the kasmvnc path.
- `go build ./...` + Go tests on `wwt`, `operator`, `api-server`;
  `tsc -b` + vitest tests on the frontend.
- i18n: any new string goes through
  `frontend/src/i18n/locales/{en,fr}.json`.
- This prompt touches the kasmvnc proxy's security surface (filtering
  requests toward a user pod): run the final diff through
  `/security-review` before considering it done, independent of unit
  tests.
- Each task (A, B, C) is independently shippable, but **B and C
  cannot be decided without A** — don't skip the live audit to jump
  straight to code.

## Open points (your call)

- The real role of `kasm_user` on its own session (owner or not) —
  determines the entirety of task A's follow-up, to be verified
  first.
- Strict allowlist of the eight endpoints unrelated to file transfer,
  vs. the documented status quo (let them pass, with the
  justification "single-user session, no third party to protect
  against the user themself") — your judgment call, to be made after
  the A.2 audit, not a technical given.
- Exact names of the clipboard DLP directives in `kasmvnc.yaml` — to
  be verified against the official docs before coding, not guessed.
- Triple state (`granted`/`denied`/`ungoverned`) vs a plain bool for
  `SessionCapabilities.ClipboardCopy/Paste` — changes the contract,
  document the choice if you introduce this triple state (same open
  point as in the original Feature 4).
