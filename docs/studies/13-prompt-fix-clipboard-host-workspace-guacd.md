# Fable 5 Prompt — Fix: host↔workspace clipboard dead in dev (guacd) — HTTPS required on the dev env

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

The kasmvnc counterpart of this bug has already been handled (commit
`afc081da`, prompt `docs/studies/14-prompt-fix-clipboard-host-workspace-kasmvnc.md`):
the conclusion there was the same — seamless clipboard requires a
**secure context** — and the kasmvnc fix therefore couldn't be
verified in dev either as long as the dev env stayed on plain HTTP. The dev
HTTPS delivered here serves both protocols.

## Symptom (fixed — the old version of this prompt was wrong)

- **Inside a workspace**, copy-paste works
  normally (copy in one remote-desktop app, paste into
  another app of the same desktop).
- **Between the local machine (host) and a workspace**, seamless clipboard
  doesn't work in **either direction**: neither copy on the host →
  Ctrl+V in the workspace, nor copy in the workspace → paste on the
  host.
- The overlay's manual fallback (`SessionOverlay.tsx`, the
  "clipboardManual" section, the two `<textarea>`s) works — the bug is
  degrading, not blocking.

## Root cause (verified on code — to confirm in browser)

The dev environment is served over **plain HTTP** on
`http://waas.127.0.0.1.nip.io:8080` (cf. `hack/dev/values-dev.yaml`:
`ingress.tls.enabled: false`; `hack/dev/k3d-config.yaml`: only port
80 of the load balancer is mapped). Now `waas.127.0.0.1.nip.io` is **not**
a secure context: browsers judge the origin by its name
(`localhost`, `*.localhost`, literal IP `127.0.0.1`), not by what
DNS resolves to — a nip.io subdomain over http doesn't count, even if it
points to loopback (`window.isSecureContext === false`).

Consequence: `navigator.clipboard` **doesn't exist at all** on this
origin, and the entire clipboard bridge on the guacd path disables itself
through its own guards — behavior already honestly documented in
`frontend/src/lib/clipboard.ts` (header L9-15):

1. **remote→host dead**: `client.onclipboard`
   (`frontend/src/components/DesktopPane.tsx:210-228`) only attempts
   `writeText` if `hasClipboardApi()` (L224) — false here.
2. **host→remote seamless dead**: `syncFromSystem()` (L254-260,
   triggered on `focus` and on connection) exits immediately on
   `canReadSystemClipboard()` — false here.
3. **host→remote via the `paste` event (L245-249) also dead**, but for
   a reason independent of HTTPS: `Guacamole.Keyboard(container)`
   (L298+) attaches its listeners in capture phase on the same `container`
   and calls `e.preventDefault()` on every interpreted key (Ctrl and V
   included — `onkeydown` returns `undefined` since `sendKeyEvent` doesn't
   return anything, treated as "block the default"). A keydown whose
   default is prevented never triggers the native editing command → the DOM
   `paste` event never fires during a real Ctrl+V
   in the pane.
4. **Why the inside-of-workspace case works**: this path never goes
   through the browser — keystrokes are relayed to the remote desktop
   which manages its own clipboard. Consistent with the
   symptom.

Point 3 explains why even the "safety net" that was supposed to work
without HTTPS doesn't save the day. But the dominant cause and the
prerequisite for everything else is the absence of a secure context: without
it, nothing is even testable.

## What needs to be delivered

### 1. HTTPS on the dev environment (main deliverable)

Goal: `https://waas.127.0.0.1.nip.io:8443` working with a
self-signed certificate — the browser warning is manually accepted
once, that's an accepted trade-off. cert-manager is **already installed**
by `make dev-up` and the chart already supports ingress TLS
(`helm/waas/templates/ingress.yaml:9-27`: `cert-manager.io/issuer`
or `cluster-issuer` annotation depending on `issuerRef.kind`,
`tls` section with `secretName: {{ .Release.Name }}-public-tls`).

- **`hack/dev/k3d-config.yaml`**: add the mapping
  `- port: "8443:443"` with `nodeFilters: ["loadbalancer"]`. For
  existing clusters (the file is only read at creation time): document
  `k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"` as
  an alternative to `make dev-reset` (the edit only recreates the
  loadbalancer container, without losing the cluster).
- **`hack/dev/values-dev.yaml`**: set `ingress.tls.enabled: true`
  with `issuerRef: { kind: Issuer, name: waas-selfsigned }` to
  **reuse** the self-signed Issuer that the chart already creates for
  the operator's webhook
  (`helm/waas/templates/operator-webhook.yaml:5-11`,
  `{{ .Release.Name }}-selfsigned`, release `waas` in dev → ingress and
  Issuer in the same namespace, the `cert-manager.io/issuer` annotation
  suffices). Watch out: this Issuer is gated by
  `operator.webhook.enabled` — check that it's active in dev (it is
  by default, the operator needs it). If it turns out to be disableable,
  fall back to a small dev-only manifest
  (`hack/dev/selfsigned-clusterissuer.yaml`, `kubectl apply` in
  `dev-deploy`) rather than coupling it.
- **Traefik (bundled with k3s)**: nothing to install — it exposes `websecure`
  (443) out of the box and will terminate TLS with the `waas-public-tls`
  secret created by cert-manager's ingress-shim. Verify after deployment that
  the `Certificate` is `Ready` and that the secret exists in the
  release's namespace.
- **HTTP must keep working**: `http://waas.127.0.0.1.nip.io:8080`
  stays the path for smoke tests (`WAAS_SMOKE_URL`, Makefile L192) and
  for the Playwright e2e check — add **no** HTTP→HTTPS
  redirect. With Traefik's ingress provider, a `tls`
  section doesn't turn off the HTTP router; verify this afterward (a
  `curl` on both ports).
- **`Makefile` `dev-url` target**: display both URLs, noting
  that seamless clipboard requires the https variant.
- If a dev doc (README, `docs/`) mentions the dev URL, update it.

### 2. Verification under real conditions (part of the deliverable)

Under `https://waas.127.0.0.1.nip.io:8443` (Chromium, certificate
interstitial accepted — the tunnel's `wss://` goes through as soon as
the origin exception is accepted):

- **remote→host**: copy text in the remote desktop → it must
  land in the host's clipboard (`writeText`, L224-226, doesn't
  ask for permission in Chromium).
- **host→remote**: copy text in an app **outside the browser** →
  come back to the tab → Ctrl+V in the pane. This is where the
  secondary diagnostic (below) splits — observe before
  coding: temporarily log the rejected promise of `readText()` instead
  of swallowing it (L259), and log whether the `paste` event fires.
- Document the protocol and the observed results in the commit or
  in `docs/studies/` — this is a browser permission model
  behavior, not simulable in jsdom.

### 3. Only if host→remote is still broken under HTTPS

The diagnostic inherited from the old version of this prompt anticipates two
problems that would survive the move to HTTPS:

- `readText()` on the `focus` event isn't within a transient user
  activation: if the `clipboard-read` permission hasn't already
  been granted, Chrome may silently reject without ever
  showing the prompt. Fix: also attempt the read within a **real user
  gesture** — the natural entry point is the `mousedown` that already
  does `container.focus()` (L289-292). Keep `focus`/connection as well
  (covers the case where permission is already granted).
- The `paste` event killed by `Guacamole.Keyboard` (diagnostic point 3):
  either honestly fix the comments that present it as a
  universal safety net (`DesktopPane.tsx:243-244`, `lib/clipboard.ts:12-13`),
  or — only if it's clean — let the browser default pass through on
  Ctrl+V without removing the keystroke relay to the remote
  desktop. The comment-only fix is a valid fix.

**Never remove** the keyboard relay (`keyboard.onkeydown`/`onkeyup`):
it's what lets the app INSIDE the workspace run its own
paste — that's the behavior that already works.

## Constraints

- Don't touch the kasmvnc path (already delivered, cf. header) — but
  take advantage of HTTPS to verify that its clipboard now works, and
  note the result.
- Don't regress the overlay's manual fallback (last resort for
  Firefox / permission refused), nor the multi-pane focus handling (a
  click only gives the keyboard to THAT pane), nor the HTTP smoke tests.
- No new dependency; no cert to install in the machine's
  trust store (the accepted browser warning is enough).
- The prod path (`ingress.tls` with a real issuer) must not change
  behavior: everything happens within `hack/dev/` + Makefile.

## Tests

- `make dev-reset && make dev-bootstrap` (or cluster edit + dev-deploy)
  passes and `dev-url` shows both URLs; `curl -k` responds with 200/302
  on both ports.
- Manual protocol from § 2 (both directions, Chromium) — documented.
- Non-regression: inside-workspace scenario intact; smoke
  (`make smoke`) still green over HTTP; existing Vitest tests
  (`DesktopPane`/`SessionOverlay`) green.

## Open points (your arbitration)

- If § 3 turns out to be necessary: document `onPaste`'s limits vs.
  make it actually functional — decide based on what you observe.
- `waas.localhost` over HTTP would also be a secure context (zero
  cert, zero warning) but was ruled out: dev HTTPS is closer
  to prod, exercises `wss://` + Traefik TLS termination, and
  remains useful for testing from another machine. Don't fall back
  to this option without a strong reason — if you see one, say
  so instead of silently switching.
</content>
