# Diagnostic — unusable VNC session (black artifacts, no input)

**Status: fixed.** Affects every guacd session (VNC **and** RDP, SSH pending):
the bug was in the WebSocket proxy, not in the desktop protocol.

## Symptoms

- The remote screen displays partially, covered with black
  patches.
- Mouse and keyboard have no effect (or clicks land offset).
- Screen freezes after a few seconds; sometimes a hard disconnect.

## Root cause

`wwt` relayed the guacd → browser stream in raw 32KB TCP chunks,
each sent as-is as a single WebSocket message (`proxy.pipe()`).

But guacamole-common-js (`Guacamole.WebSocketTunnel.onmessage`) parses **each
WebSocket message as a self-contained sequence of complete
instructions** — this is an invariant of Guacamole's WebSocket transport,
guaranteed in the official stack by the server-side tunnel that splits
at instruction boundaries. A message cut mid-instruction produces:

- at best, garbage elements silently ignored by the client →
  drawing instructions (`png`/`blob`) lost → **black patches**;
- loss of `sync`/acks → guacd stops sending frames → **freeze**;
- at worst `close_tunnel("Incomplete instruction")` → the tunnel silently
  drops all further sends → **no more mouse or keyboard** while
  the canvas remains displayed.

Any frame > 32KB (guaranteed from the very first 1920×1080 framebuffer)
triggered the issue. Small updates got through — hence a session that
"almost worked".

### Secondary defects fixed at the same time

1. **Mouse coordinates not compensated for scale** (`DesktopPane.tsx`): the
   display is scaled to the panel (`display.scale(...)`) but mouse
   events were sent in screen pixels → offset clicks. Coordinates are now
   divided by `display.getScale()`.
2. **Internal tunnel ping forwarded to guacd**: the JS tunnel emits an
   internal instruction every 500ms (empty opcode: `0.,4.ping,…;`) meant for
   the tunnel *endpoint*, which must reply with an identical ping. wwt was
   passing it through raw to guacd. It now intercepts it: echoed back to the browser,
   never forwarded.
3. **TigerVNC blacklist** (fixed separately, commit `c21d40c`): the
   Kubernetes TCP probe counted as a VNC auth failure; on the 5th, TigerVNC
   blacklisted the source and also blocked guacd → connections refused.
   `-UseBlacklist=0` (ClusterIP port only, gated by the connection token).

## Fix

- `wwt/internal/guac/framing.go`: `Framer` buffers the guacd stream and only emits
  WebSocket messages that end on an instruction
  boundary (scanning length prefixes, counted in Unicode code points, without a
  full parse). Corrupted stream ⇒ clean session close.
- `wwt/internal/proxy/proxy.go`: `pipe()` uses the `Framer` (guacd→ws),
  intercepts internal `0.` messages (ws→guacd) and echoes pings.
- `frontend/src/components/DesktopPane.tsx`: scale-aware mouse mapping.

## Reproduce / validate

1. **Repro (before fix)**: open a 1920×1080 VNC session with a loaded
   wallpaper. DevTools → Network → WS: some incoming messages don't
   end with `;`; console: `Incomplete instruction` possible.
2. **Validation (after fix)**:
   - every incoming WS message ends with `;` and starts with a valid
     length prefix;
   - no more black patches after a full redraw (drag a window across
     the whole screen);
   - mouse accurate on all four corners at different scales (resize
     the panel / split view); keyboard OK after clicking in the panel;
   - guacd logs (`guacd -L debug`): receiving `key`/`mouse` events,
     no unknown "ping" instruction.
3. **Automated regression**: `wwt/internal/guac/framing_test.go` (adversarial
   byte-by-byte splits, multi-byte runes) and
   `wwt/internal/proxy/proxy_test.go::TestFramesEndOnInstructionBoundaries`
   (fake guacd that "drips" a large instruction + ping echo + the ping
   never reaches guacd).
4. **RDP**: same code path, same fix; run through the same checklist
   (artifacts, input, mouse accuracy) on an RDP session to confirm.
