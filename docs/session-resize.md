# Dynamic VNC/RDP session resize — a WaaS mechanism, not guacd

Resizing an in-cluster desktop mid-session **does not** go through
Guacamole's native resize. Don't look for `sendSize()` in the guac
tunnel or for any effect from the RDP `resize-method` parameter: both
are dead ends in this architecture.

## Why the native path is dead

- **VNC**: guacd's VNC client never emits a resize mid-session (no
  client→server `size` on this protocol).
- **RDP**: `resize-method=display-update` would talk to the RDP
  server — but our RDP server is the xrdp-libvnc bridge, which cannot
  propagate a resize down to the underlying Xvnc
  (`waas-images/.../waas-resize`, header comment).
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
  only **in-cluster vnc/rdp** sessions call the endpoint.
  kasmvnc resizes natively in its own client
  (`resize=remote`), ssh has no desktop, remote workspaces have
  no pod (explicit 400 server-side).
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
needed**: WaaS resize goes through pod-exec (diagram above),
which works identically for VNC and RDP since both run
on the same Xvnc in the WaaS images (RDP = xrdp bridge to that Xvnc).
So there's nothing to "enable" on the guacd side to bring VNC up to
RDP's level — it's already symmetric, regardless of the guacd version.
Implementing native #469 *in addition* (lower latency than an exec, or
scenarios where exec isn't possible) would be a separate, not-yet-started
undertaking.

## Fate of `resize-method` (2026-07-10 decision: kept)

`resize-method` (RDP registry, tier ui) stays inert for
in-cluster desktops: the pod-exec mechanism bypasses it entirely. It is
**kept** because for *remote workspaces* RDP, guacd talks to
a real external RDP server and this parameter then drives guacd's
native negotiation. Its description in the registry
(`operator/pkg/params/params.go`) now explicitly states this
boundary.

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
