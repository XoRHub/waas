# Dynamic VNC session resize — a WaaS mechanism, not guacd

Resizing an in-cluster Linux desktop mid-session **does not** go
through Guacamole's native resize. Don't look for `sendSize()` in the
guac tunnel: it is a dead end in this architecture.

## Why the native path is dead

- **VNC**: guacd's VNC client never emits a resize mid-session (no
  client→server `size` on this protocol).
- **TigerVNC**, on the other hand, supports RandR `SetDesktopSize`:
  resolution CAN be changed live, but only *from inside the pod* —
  which is exactly what the `waas-resize WIDTHxHEIGHT` script does (xrandr).

## The real mechanism

```
browser (ResizeObserver, debounced ~500ms)
  → POST /api/v1/workspaces/{id}/resize {width, height}   (api-server)
    → exec `waas-resize WxH` in the pod (client-go SPDY, fixed argv)
      → xrandr / RandR SetDesktopSize on Xvnc
        → guacd sees the framebuffer change and follows naturally
```

- Frontend: `frontend/src/lib/sessionResize.ts` (debounce + gating) —
  only **in-cluster vnc** sessions call the endpoint. kasmvnc resizes
  natively in its own client (`resize=remote`); in-cluster rdp is a
  windows KubeVirt VM with no pod to exec `waas-resize` in, so the
  frontend never calls for it (guacd-native `resize-method` is the only
  candidate there, unverified — see below); remote workspaces have no
  pod either (explicit 400 server-side).
- api-server: `internal/service/workspace_resize.go`. Authorization =
  `fetchByID` (owner or admin), workspace `Running` required (409
  otherwise), 100–7680 bounds validated before any resolution, pod
  resolved via the `waas.xorhub.io/workspace` label in the placement
  namespace. **Fixed** command (`waas-resize WxH`), never a shell;
  `waas-resize` re-validates its argument inside the pod.
- RBAC: `pods/exec` (verb `create`) is a dedicated entry in the
  api-server ClusterRole (`helm/waas/templates/api-server/roles.yaml`) —
  deliberately kept separate from the read-only `get/list` to stay
  visible in review.
- Audit: every effective resize writes `workspace.resized` (name + mode).

## Why guacamole-server PR #469 isn't relevant here

PR #469 (guacd 1.6) adds native guacd↔VNC server negotiation of a
server-initiated resize. Our guacd is already on 1.6
(`helm/waas/values.yaml`), but this path is **neither used nor
needed**: WaaS resize goes through pod-exec (diagram above), RandR on
the image's own Xvnc. So there's nothing to "enable" on the guacd side,
regardless of the guacd version. Implementing native #469 *in
addition* (lower latency than an exec, or scenarios where exec isn't
possible) would be a separate, not-yet-started undertaking.

## Fate of `resize-method` (2026-07-10 decision: kept)

`resize-method` (RDP registry, tier ui) was inert for the linux
desktops' RDP (the xrdp-libvnc bridge could not propagate
`display-update` down to Xvnc — the pod-exec mechanism bypassed it
entirely) and was **kept** because for *remote workspaces* RDP, guacd
talks to a real external RDP server and this parameter then drives
guacd's native negotiation. In-cluster `rdp` is now windows-only, i.e.
also a real RDP server, where the same native path applies in
principle — unverified, and outside the pod-exec mechanism. Its
description in the registry (`operator/pkg/params/params.go`)
explicitly states this boundary.

## guacd / guacamole-common-js versions

guacd is on 1.6.0; the frontend stays on `guacamole-common-js@^1.5.0`
**deliberately**: Apache doesn't publish this lib on npm — the package
`guacamole-common-js` as well as `@glokon/guacamole-common-js` (the only
1.6.x mirror) are third-party mirrors, and we refuse to introduce a
non-Apache source into the frontend's supply chain. The consumed API
(`DesktopPane.tsx`: Tunnel/Client/Mouse/Keyboard/Streams)
is stable between 1.5 and 1.6, and guacd 1.6 remains compatible with
1.5 clients. If alignment becomes necessary, the acceptable path is
to vendor the official Apache build (Maven Central
`org.apache.guacamole:guacamole-common-js`).
