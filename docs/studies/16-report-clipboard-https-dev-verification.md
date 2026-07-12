# Report: HTTPS on the dev env + clipboard verification in a real browser

Deliverable for prompt `13-prompt-fix-clipboard-host-workspace-guacd.md`
(HTTPS also serves to verify the kasmvnc fix `afc081da` from prompt 14).
Verified on 2026-07-10 on the k3d cluster `waas-dev`.

## What was delivered

- `hack/dev/k3d-config.yaml`: mapping `8443:443@loadbalancer` (+ note:
  `k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"` for an
  existing cluster — the file is only read at creation time).
- `hack/dev/values-dev.yaml`: `ingress.tls.enabled: true` with
  `issuerRef: {kind: Issuer, name: waas-selfsigned}` — reuses the
  self-signed Issuer that the chart already creates for the operator's
  webhook (`operator.webhook.enabled` is active by default and
  necessary in dev). No chart-side change:
  `helm/waas/templates/ingress.yaml` already supported everything
  (cert-manager annotation + `tls` section).
- `Makefile` `dev-url`: shows both URLs (https = seamless clipboard,
  http = smoke tests).
- Fixed comments (`DesktopPane.tsx`, `lib/clipboard.ts`) and
  `docs/clipboard.md` updated: the DOM `paste` event is NOT a universal
  safety net (see diagnostic below).

Prod path unchanged: `values.yaml` and the templates don't move.

## Infra verified

- `Certificate waas-public-tls` → `Ready: True`, `kubernetes.io/tls`
  secret created by the ingress-shim, SAN `DNS:waas.127.0.0.1.nip.io`,
  90 days.
- Traefik (bundled with k3s) terminates TLS on `websecure` with nothing
  to install.
- `curl`: 200 on `http://…:8080/` AND `https://…:8443/` — **no
  HTTP→HTTPS redirect** (a Traefik Ingress's `tls` section doesn't
  turn off the HTTP router); API login OK on both.
- `make smoke` (HTTP, real sessions per protocol): green afterward.

## Browser verification protocol (real Chromium, Playwright)

Not simulable in jsdom: browser permission model + real guacd session.
Tooling: Chromium driven by Playwright
(`ignoreHTTPSErrors: true` = the programmatic equivalent of
accepting the certificate interstitial; `clipboard-read`/
`clipboard-write` permissions granted to the origin = the "prompt
accepted" state). On the workspace side, the X clipboard (CLIPBOARD
selection) is read/written via `kubectl exec`: image `ubuntu-xfce` via
two python-xlib scripts copied into the pod (the image has neither
xclip nor xsel), kasm image via its embedded `xclip`. TigerVNC
(`vncconfig -nowin`) natively syncs X selections ↔ VNC cut-text.

Workspaces: `ubuntu-xfce` (vnc/guacd) and `kasm-terminal` (kasmvnc),
created via the API then deleted after verification.

## Results on `https://waas.127.0.0.1.nip.io:8443`

| Check | Result |
|---|---|
| `window.isSecureContext`, `navigator.clipboard`, `readText` | ✅ present |
| guacd host→remote: `writeText` + `focus` event → pod's X CLIPBOARD | ✅ identical text |
| guacd remote→host: X CLIPBOARD set in the pod → browser `readText` | ✅ identical text |
| kasmvnc host→remote (iframe, `clipboard_*` params from fix `afc081da`) | ✅ |
| kasmvnc remote→host | ✅ |
| Control check `http://…:8080`: `isSecureContext` | ❌ `false`, no `navigator.clipboard` (expected) |

The tunnel's `wss://` goes through as soon as the origin exception is accepted.

## Diagnostic § 3: the `paste` event, settled by observation

Live instrumentation (`paste` listener in capture phase on the pane +
`keydown` listener in bubble phase) then a real Ctrl+V in the pane:

```
{"pasteFired": false, "keydownPrevented": true}
```

Confirmed: `Guacamole.Keyboard` calls `preventDefault()` on the relayed
keydown (the `undefined` return of `onkeydown` counts as "block the
default"), so the native paste command never runs and the `paste`
event never fires. The event stays wired as a theoretical safety net but
the comments presenting it as a universal path ("works
everywhere, HTTP included") were wrong — fixed.

**Arbitration: comment fix, no Ctrl+V pass-through.**
Letting the browser default pass through on Ctrl+V would fire the
`paste` event (which sends the clipboard stream) AFTER the keydown already
relayed to the desktop: the remote app would paste stale
content on the first Ctrl+V. Host→remote seamless works via
focus-sync (verified above), which primes the remote clipboard
BEFORE the keystroke; Firefox and permission-denied cases keep the
overlay's manual exchange. The `readText` on focus also didn't need
the `mousedown` gesture: permission granted = read OK outside transient
activation (the state after the first Chromium prompt is accepted).

## Trap encountered (worth remembering)

The first kasmvnc pass failed: the frontend image deployed in k3d
predated `afc081da` — the iframe URL didn't have the
`clipboard_up/down/seamless` params. A `docker build` + `k3d image
import` + `rollout restart` of the frontend was enough. Generic symptom:
a frontend fix "committed but not reloaded" is invisible in dev
(same drift as the netpol lockout documented in the Makefile).

## Reproduce

```sh
make dev-url                 # both URLs
# existing cluster: k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"
# then make dev-deploy ; new cluster: make dev-reset && make dev-bootstrap
kubectl -n waas get certificate waas-public-tls   # Ready: True
curl -sk -o /dev/null -w '%{http_code}\n' https://waas.127.0.0.1.nip.io:8443/
```

Manual verification: open the https variant in Chromium, accept
the certificate warning once, connect to a vnc workspace;
copy on the host → come back to the tab → Ctrl+V in an app on the
remote desktop; copy in the desktop → paste on the host.
</content>
