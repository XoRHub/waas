# Fable 5 Prompt — Feature 9: the "Leave Session" bar blocks the XFCE Applications menu at the top of the screen

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

In a VNC workspace running an XFCE desktop (Ubuntu image), the XFCE panel displays its "Applications" menu at the top left of the remote screen. This menu is currently impossible to click: the hover/click rectangle of the WaaS frontend's "Leave Session" bar spans the full width of the screen at the top, and intercepts clicks even where nothing is visually displayed.

## What already exists (bug precisely located)

The component at fault is **not** `SessionOverlay.tsx` (which is the settings menu at the bottom right, `absolute bottom-4 right-4`, a small 36×36 button — unrelated to this bug). The real culprit is the "Leave Session" bar rendered inline in `DesktopView`, `frontend/src/pages/ConnectPage.tsx:216-224`:

```tsx
{state === 'connected' && (
  <div className="group absolute inset-x-0 top-0 z-10 flex justify-center">
    <div className="absolute top-0 h-2 w-40 rounded-b-md bg-white/20 transition group-hover:opacity-0" />
    <div className="-translate-y-full rounded-b-lg bg-slate-900/90 px-4 py-2 text-sm text-white shadow-lg backdrop-blur transition-transform duration-200 group-hover:translate-y-0">
      <button onClick={leave} className="font-medium text-blue-400 hover:text-blue-300">
        {t('connect.leave')}
      </button>
    </div>
  </div>
)}
```

**Root cause**: the outer `<div>` has `absolute inset-x-0 top-0` — so it spans **the full width of the viewport**, pinned to the top. Its child (the "Leave Session" label) is only moved off-screen by a `transform: translateY(-100%)` (`-translate-y-full`), which is a **paint-only** effect — it does not remove the element from the flow nor shrink the parent's hit-test box. Result: the outer wrapper keeps a full-width clickable zone, about 36-40px tall (the label's height), pinned to the top of the screen, **even when the label is visually off-screen and only the small centered pull-tab (`w-40`, 160px) is visible**. Since this wrapper does not have `pointer-events-none`, it defaults to `pointer-events: auto` and intercepts all clicks across this entire band — including the top-left corner where the XFCE Applications menu lives. `z-10` places it above `DesktopPane` (no `z-index`/`pointer-events-none` counter-measure found in that component).

**History**: this structure (wrapper `absolute inset-x-0 top-0` + label hidden via transform) was introduced in commit `01496d16705a` ("split view, workspace folders, protocol settings and theme toggle") when `ConnectPage` became a thin wrapper around the new `DesktopPane`, and has never been touched since (`3f7f25053652`, `c99623d0e4ce`, `f8558abf685d`). It has never been scoped by session type — it applies identically to all in-cluster sessions (kasmvnc/remote are excluded elsewhere for other reasons, but not for this one).

## What needs to be delivered

Fix the hit-test zone so it matches the actually visible/interactive area:

1. Add `pointer-events-none` on the outer `<div>` (`absolute inset-x-0 top-0`, line 217).
2. Explicitly add `pointer-events-auto` on the inner element(s) that are actually clickable/hoverable — at minimum the small visual pull-tab (line 218) to capture the hover that triggers `group-hover`, and the label+button block (line 219-223) so the "Leave Session" button stays clickable once shown.
3. After the fix, verify that:
   - hovering the pull-tab (the 160px centered at the top) still reveals the label and stays clickable,
   - the "Leave Session" button stays clickable while the label is visible,
   - a click anywhere else on the top band (particularly the left corner and the right corner) now passes through to the remote content (XFCE, or any other desktop) without interception.

## Constraints to respect

- CSS/Tailwind fix targeted at this component (`ConnectPage.tsx:216-224`) — do not touch `SessionOverlay.tsx` (a different component, unrelated to this bug).
- The existing hover/transition behavior (the pull-tab that reveals the label on hover) must remain identical after the fix — this is a click-zone fix, not a design change.
- Add a test (vitest + testing-library if the repo's convention allows it for this kind of layout, or at minimum a test that checks for the presence of `pointer-events-none` on the outer wrapper and `pointer-events-auto` on the interactive inner elements, to avoid a silent regression if someone touches this JSX again later).
- Manually test on the k3d dev environment (`make dev-up dev-build dev-load dev-deploy`, a real XFCE VNC workspace) that the Applications menu at the top left becomes clickable again — this bug is not detectable by a unit test alone since it depends on overlapping with real remote content displayed by the Guacamole canvas.

## Open points (your call)

- Should the `w-40`/the visual pull-tab width itself also be restricted, or is fixing only `pointer-events` on the wrapper enough (which already suffices to unblock clicks elsewhere, without changing the visual design) — recommendation: change only `pointer-events`, leave the centered visual pull-tab unchanged.
