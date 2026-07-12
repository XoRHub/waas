# Fable 5 Prompt — Fix (to confirm): host↔workspace copy-paste broken on the kasmvnc side, in both directions

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

**This prompt is deliberately an investigation before being a
fix.** The report says: on the `kasmvnc` protocol, no
copy-paste works between the local machine (host) and the
workspace, **in both directions** (host→workspace AND workspace→host).
There are several plausible causes, of very different natures (a
WaaS code bug, missing config/policy in the test environment,
a limitation intrinsic to the KasmVNC client itself, out of this repo's
reach) — **don't write a fix before having isolated which
one actually applies**. A fix that would mask a misconfigured
policy problem, for instance, would be worse than no fix at all.

## Repo context: what handles kasmvnc clipboard today

The `kasmvnc` protocol does NOT go through the guacd tunnel: `DesktopPane.tsx`
detects `result.protocol === 'kasmvnc'` (`DesktopPane.tsx:~150-165`) and
mounts an `<iframe>` pointing at the KasmWeb web client, reverse-proxied
by wwt under `/kasm/{session}/...` (same origin as the rest of
the app). **This iframe currently has no `allow` attribute**
(`DesktopPane.tsx:342-350`):

```tsx
<iframe
  src={kasmUrl}
  title={t('connect.desktopFrame', 'Remote desktop')}
  className={state === 'connected' ? 'h-full w-full border-0' : 'hidden'}
  onLoad={...}
/>
```

Everything else about kasmvnc clipboard (system clipboard
read/write, copy-paste UI) lives **inside the KasmWeb client
itself** (third-party code served by the KasmVNC container, absent from
this repo) — WaaS only acts on two levers, both
within this repo:

1. **The proxy** (`wwt`) that reverse-proxies the iframe — could
   potentially break headers needed (see §1 below).
2. **The clipboard DLP policy**, derived from `WorkspacePolicy.Clipboard`
   and stamped into `~/.vnc/kasmvnc.yaml` by the controller:
   - `operator/pkg/kasmcfg/kasmcfg.go:20-24` — the 3 managed keys:
     `data_loss_prevention.clipboard.server_to_client.enabled` (direction
     workspace→host, "copy from the workspace"),
     `data_loss_prevention.clipboard.client_to_server.enabled` (direction
     host→workspace, "paste into the workspace"), and
     `runtime_configuration.allow_client_to_override_kasm_server_settings`
     forced to `false`.
   - `operator/internal/controller/kasm_config.go:80-104` (`applyClipboardPolicy`)
     stamps these values from `kasmClipboardGrant` (`kasm_config.go:70-79`),
     itself based on `policy.ClipboardOf(pol)` (`operator/pkg/policy/policy.go:161-171`).
   - **Critical point**: `policy.ClipboardOf` returns `(true, true)`
     (everything allowed) if a policy resolves but doesn't define
     `Clipboard`, **but returns `(false, false)` if NO policy
     resolves for the workspace's identity** (`kasmClipboardGrant`,
     comment "Resolution failure fails closed: no policy match
     means clipboard off"). A test environment without a default
     policy covering the tested user would therefore disable BOTH
     directions of kasmvnc clipboard **by design**, not by bug.

## What needs to be delivered

### 1. Isolate the cause before any fix

On the environment where the bug is observed (dev k3d or other), for THE
kasmvnc workspace in question:

1. Read the workspace's effective ConfigMap (endpoint mentioned in
   project memory `/kasmvnc-config`, or directly
   `kubectl get configmap <workload-name> -o yaml` in the workspace's
   namespace) and check the real state of
   `data_loss_prevention.clipboard.server_to_client.enabled` and
   `.client_to_server.enabled`.
   - **If either or both are `false`**: trace back to the
     applicable `WorkspacePolicy` (or the absence of resolution,
     fail-closed) via `policy.Resolve` (`operator/pkg/policy/policy.go`).
     If it's a missing default policy in the test
     environment, **this is not a bug in this repo** — document it as such
     and stop there for this sub-case (fail-closed is a security
     choice already decided, don't relitigate it here); if instead you
     identify a case where the policy SHOULD resolve and doesn't
     (matching bug), that's a legitimate bug — fix it and
     document the exact case that triggers it.
2. **If both keys are indeed `true`** (clipboard allowed in both
   directions by the policy) and copy-paste still fails:
   the cause is elsewhere, in the browser ↔ iframe ↔ KasmWeb client
   path. Look then at the two leads below.

### 2. Concrete lead: the iframe lacks clipboard permission

The KasmWeb iframe (`DesktopPane.tsx:342-350`) has no `allow` attribute.
Without `allow="clipboard-read; clipboard-write"`, the browser can
deny the iframe's content access to `navigator.clipboard` even
when the parent page itself has access — this is a
Permissions Policy behavior independent of direction (host→workspace AND
workspace→host), which would match exactly the report ("both
directions, kasmvnc protocol regardless").

- Add `allow="clipboard-read; clipboard-write"` to the `<iframe>` and
  **test under real conditions** (Chromium + HTTPS) whether it's enough to
  unblock one direction, both, or neither.
- If it fixes the problem, that's the whole fix needed for this
  prompt — don't complicate beyond that.
- If it isn't enough, document what you observe (which direction works,
  which one doesn't, console error message if any on the
  KasmWeb client side): the next step depends on the KasmWeb client
  itself, outside this repo.

### 3. Lead NOT to dig into beyond reason: the KasmWeb client itself

The KasmVNC web client's code (in the container image, not in this
repo) handles its own clipboard synchronization. If §1 and §2
don't explain the symptom, WaaS has no more direct lever — don't
go into reverse-engineering the KasmWeb client or a
fix that would work around its internal behavior (e.g. injecting
JS into the iframe, cross-origin hacking). Document the finding
(probable cause outside the repo, e.g. an upstream KasmVNC
limitation/bug) and propose an escalation (upstream ticket, or mitigation via
user docs) rather than a fragile workaround fix.

## Constraints

- Don't touch the guacd path (separate prompt,
  `docs/studies/13-prompt-fix-clipboard-host-workspace-guacd.md`) — this
  prompt only concerns `kasmvnc`.
- Don't relitigate the fail-closed behavior of `policy.ClipboardOf`
  (`operator/pkg/policy/policy.go:161-171`) — this is a security
  choice already decided (Feature 11, now documented in
  `docs/kasmvnc.md`), not a
  bug.
- If you add `allow` to the iframe, check that it doesn't release
  more permissions than necessary (limit yourself to `clipboard-read` and
  `clipboard-write`, don't add other features as a precaution).

## Tests

- Manual browser verification (Chromium, HTTPS or localhost),
  on a kasmvnc template whose policy explicitly allows both
  directions: copy an app outside the browser → paste into the
  kasmvnc workspace, then the reverse (copy in the workspace → paste
  outside the browser). Precisely document which direction works after
  your change.
- Non-regression: a template/policy that forbids one direction (or
  both) keeps effectively blocking it in the container (the
  fail-closed and the stamped DLP keys stay unchanged if you only
  touch the iframe).
- No unit test expected for the iframe/permissions part
  (real browser behavior, not simulable in jsdom); if you
  fix a policy resolution bug (§1), add the corresponding Go test
  in `operator/internal/controller` or
  `operator/pkg/policy`.

## Open points (your arbitration)

- The sub-case that actually applies (missing policy in the
  test environment / iframe permission / KasmWeb client
  limitation) can only be settled by reproducing — don't anticipate
  which before having observed it.
- If the fix is limited to the iframe's `allow` attribute, no new
  section needs to be added to the overlay; if you identify that a
  manual fallback like `SessionOverlay.tsx` (cf. the guacd path) would make
  sense for kasmvnc, note it as a lead rather than implementing it
  without being asked to — integrating with an embedded KasmWeb
  client in an iframe isn't trivial (WaaS doesn't control its internal DOM).

---

## Investigation result (2026-07-10, Fable 5)

**Root cause identified and fixed: the KasmWeb client disables its own
clipboard by itself when running inside an iframe.** In the UI bundle of the
kasmweb 1.19 images, `window.self !== window.top` without `show_control_bar`
as a query param causes `clipboard_up`, `clipboard_down` and
`clipboard_seamless` to initialize to `false` — copy-paste dead in both
directions, regardless of the policy's state. These settings are read
first from the `vnc.html` URL.

**Fix (a single change, `frontend/src/components/DesktopPane.tsx`)**:
added `clipboard_up=1`, `clipboard_down=1`, `clipboard_seamless=1` to
the kasmvnc iframe's query params.

**Prompt hypotheses, settled by reproduction (dev k3d, Chromium/Playwright)**:

1. **DLP policy (§1): ruled out.** The test workspace's effective
   ConfigMap checked: both keys `enabled: true` (the `admins` policy
   resolves, `Clipboard` undefined → `(true, true)`).
2. **`allow` attribute on the iframe (§2): not needed, not added.**
   The iframe is same-origin (`/kasm/...` relative): the parent's
   Permissions Policy is inherited — measured `allowsFeature('clipboard-read'/'write')
   === true` in the iframe with no attribute.
3. The wwt proxy touches neither Permissions-Policy nor response headers.

**e2e validation (headless Chromium, localhost origin = secure context,
params injected before the first connection)**:

- host→workspace: `navigator.clipboard.writeText` on the host side → text
  read by `xclip -o` in the container. **PASS.** (The client only re-reads
  the local clipboard after a window blur/focus followed by a user
  event — that's the natural gesture: "copy elsewhere, come back,
  click.")
- workspace→host: `xclip -i` in the container → `navigator.clipboard.readText`
  on the host side. **PASS.**
- **DLP non-regression**: `admins` policy set to deny/deny → after reload
  (policy drift only applies at a resume/reload boundary, by design),
  both directions stay blocked despite the client params. The container
  DLP remains authoritative (`-IgnoreClientSettingsKasm 1` +
  `allow_client_to_override…: false`).

**Documented limitations (out of scope for the fix)**:

- **Secure context required**: on `http://waas.127.0.0.1.nip.io:8080`
  (dev, non-localhost HTTP) `navigator.clipboard` doesn't exist at all
  (`isSecureContext: false`, both parent AND iframe) — no clipboard
  possible, fix or not. In HTTPS prod or in dev via `localhost`, OK. This
  is an environment limitation, not a repo bug.
- **Firefox/Safari**: the KasmWeb client forces `clipboard_seamless` off
  (upstream UA detection); with the Kasm control bar hidden, there's no
  manual UI either. Lead (not implemented, to be arbitrated): a
  manual fallback like the guacd SessionOverlay, or exposing the Kasm
  control bar.
- Side observation: the controller doesn't watch WorkspacePolicy — a
  policy change is only re-stamped into the ConfigMap at the next
  workspace reconciliation, and the pod only rolls at resume/reload
  (a session stability choice documented in `workload.go`).
</content>
