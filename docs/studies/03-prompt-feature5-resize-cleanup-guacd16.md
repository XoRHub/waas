# Fable 5 Prompt — Feature 5: cleanup of resize-method (RDP) + alignment of guacd 1.6 / guacamole-common-js

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context — and a premise to correct before coding

The initial request for this feature was "enable native guacd 1.6 resize for VNC as well as RDP (Apache guacamole-server PR #469)". **This premise is false in this repo — verified before writing this prompt.** The session resize mechanism, delivered by commit `906017f3272e feat(resize): real end-to-end VNC/RDP session resize via pod exec`, **does not go through guacd's native resize at all**. It already works in a strictly identical way for VNC and RDP, and therefore does not depend at all on the guacd version nor on PR #469.

## What already exists (know this before coding)

**The real mechanism**: `api-server/internal/service/workspace_resize.go:52-88` (`Resize`, handler `api-server/internal/handler/workspace_handler.go:209`) executes `waas-resize WxH` **in the workspace's pod via `client-go`/SPDY exec** (`api-server/internal/k8s/exec.go`) — not a guacd message. On the frontend side, `frontend/src/lib/sessionResize.ts:16-52` observes the container via `ResizeObserver` (debounce, dedup) and calls this endpoint; `DesktopPane.tsx:297-311` wires it into the component's lifecycle.

**VNC is already at the same level as RDP**: `sessionResize.ts:27` — `const active = kind === 'workspace' && (protocol === 'vnc' || protocol === 'rdp');`. There is **no VNC-specific restriction** in the frontend, no toggle that disables resize for VNC — it's already generic. Only `kasmvnc` and remote workspaces (`kind === 'remote'`) are excluded from it (`DesktopPane.tsx:151-167` for kasmvnc; an explicit 400 in `workspace_resize.go:60-67` for remote).

**Why this already works for VNC without native guacd**: `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-resize:4-9` documents the real reason — "Guacamole's VNC client does not push browser-window resizes to the server... TigerVNC does support RandR SetDesktopSize... from inside the session... or by anything that can exec into the pod." VNC and RDP both run on the same underlying Xvnc in the WaaS images (RDP = xrdp bridging to that same Xvnc), so the `xrandr`/RandR executed via pod-exec works identically on both sides — **independent of guacd and its version**. guacamole-server's PR #469 (native guacd↔VNC-server negotiation for a server-initiated resize) is a totally different path, not implemented here, and not needed for the current functionality.

**The only leftover concerned with guacd**: `resize-method` (`operator/pkg/params/params.go:145-148`, `Enum: ["display-update","reconnect"]`, RDP only, `TierUI`). The message of commit 906017f3272e already flags it as "effectively dead" — VNC, for that matter, has no `resize-method` parameter in the registry (no entry exists for that protocol).

**guacd/guacamole-common-js versions**: `helm/waas/values.yaml:145` → `guacamole/guacd:1.6.0` (already 1.6, already above PR #469). `frontend/package.json:18` → `"guacamole-common-js": "^1.5.0"` — **behind** guacd. This is the real gap identified by the audit (`docs/studies/audit-2026-07.md` §6 technical debt).

**Existing tests**: `api-server/internal/service/workspace_resize_test.go` (auth, bounds, phase, remote-rejection, exec failure), `frontend/src/lib/sessionResize.test.ts` (debounce, gating incl. protocol whitelist, dedup, cancel).

## What needs to be delivered

This feature is a **debt cleanup + a dependency update**, not a new resize feature:

### A. Decide the fate of `resize-method`

Two defensible options, pick one and document it:

1. **Remove it** from the registry (`operator/pkg/params/params.go:145-148`) since it no longer has any real effect on the current resize mechanism (pod-exec makes it useless), with migration: verify that no template/CR in prod/dev references it in `Params`/`UserParams` before removal (grep `resize-method` in `hack/dev/templates-dev.yaml`, `gitops/`).
2. **Keep it** but rephrase its `Description` to explicitly say it no longer drives resize (WaaS handles resize via exec, this parameter only influences guacd's native behavior if a client outside WaaS ever relied on it) — only if you identify a reason to keep it (e.g. compatibility with a third-party guacd client).

### B. Align `guacamole-common-js` with guacd 1.6

Bump `guacamole-common-js` from `^1.5.0` to the latest compatible 1.6.x version in `frontend/package.json:18`. Check the library's changelog for any breaking API change affecting `DesktopPane.tsx`/canvas rendering, and run the frontend test suite + a manual connection test (via `make smoke` on the k3d dev env) after the version bump.

### C. Document the resize architecture (new or existing)

If `docs/session-resize.md` already exists (the doc-comment of `Resize()` refers to it, `workspace_resize.go:42-47`), check that it clearly explains: (1) the mechanism is pod-exec, not native guacd; (2) that's why VNC and RDP are already symmetric; (3) why guacamole-server's PR #469 is not relevant to this mechanism. If this doc doesn't exist or doesn't address these points, complete it — this is the confusion at the root of this feature, avoid it recurring.

## Constraints to respect

- Zero expected user-facing behavior change: VNC/RDP resize already works, this feature must not break anything on that path (run `workspace_resize_test.go` and `sessionResize.test.ts` without regression).
- If you remove `resize-method` (option A.1), update `docs/guacd-parameters.md` (generated by `make docs-params`, never edit it by hand) by re-running the generation.
- `go test ./...` on `operator`/`api-server`, `tsc -b` + vitest tests on the frontend.

## Open points (your call)

- Removal vs rephrasing of `resize-method` (§A) — first check whether there is a real usage before deciding to remove it.
  DECISION: rephrasing, because it may still be used for remote workspaces.. so don't remove it
- If, after this investigation, you identify a real product benefit to implementing native guacd↔VNC resize (PR #469) **in addition to** the current pod-exec mechanism (for example: reducing latency compared to an exec, or supporting a scenario where exec isn't possible) — this would be a **separate and significantly heavier** undertaking (guacd-side negotiation + VNC/TigerVNC server-side support), out of scope for this "cleanup" feature. Do not undertake it here; note it as a future direction if you judge it relevant.
