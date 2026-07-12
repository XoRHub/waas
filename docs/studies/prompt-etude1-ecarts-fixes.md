# Fable 5 Prompt — closing the UI/reality gaps from the protocol × feature study

Paste this document as-is as an implementation prompt. It assumes that you (Fable 5) have no prior conversation context.

## Context

`docs/studies/protocol-feature-matrix-2026-07-10.md` (shipped 2026-07-10) cross-references the 4 connection paths (VNC/RDP/SSH via guacd, KasmVNC via raw HTTP reverse-proxy) with cross-cutting features, entirely sourced from the code. Its final section, **"Gaps vs. what the UI implies"**, lists 6 places where the portal promises something the system doesn't deliver (or the reverse). This prompt handles them in order of increasing complexity, not in the source document's order of severity — read the source section anyway before coding, it contains the evidence (file:line) this prompt summarizes.

Treat each task as an independent increment, separate commit if possible. Tasks A and B touch no file in common with C/D/E. C, D, and E all touch `waas-images/` but distinct paths (VNC audio for C, xrdp/sesman for D, no image file at all for E — E is backend + frontend).

**Decisions already settled (don't reopen them)**:
- KasmVNC is explicitly refused on remote workspaces (task A).
- RDP clipboard/audio requires an architectural investigation before any code (task D) — implement only if you can do so without regressing the documented hardening; otherwise document and stop.
- Dynamic resize (task E) must be a real end-to-end mechanism, not a simple frontend `sendSize()` — the current architecture wouldn't support that (see task E).

---

## Task A (the simplest): refuse KasmVNC on remote workspaces

**Gap #5 of the study.** The code today accepts kasmvnc as a protocol for a remote workspace (`api-server/internal/service/remote_workspace_service.go:174`, `normalizeRemoteProtocols` validates against `params.Protocols()` with no exclusion), resolution applies `kasmDefaults` (`workspace_service.go:810`), and the frontend routes `kind=remote` + `kasmvnc` to the iframe (`DesktopPane.tsx:149`). But neither the docs (`docs/remote-workspaces.md:4-5`) nor the model comment (`api-server/internal/model/model.go:82-84`, "reachable through guacd") mention kasmvnc, and this combination is exercised by no test — **never verified in a live session**. Decision: kasmvnc stays an in-cluster-only protocol (the `wwt/internal/kasm` reverse-proxy targets a KasmVNC server co-located in the cluster; the "external machine" semantics has no kasm equivalent without an additional component, and nobody today can guarantee it works).

**To do**:
1. In `normalizeRemoteProtocols` (`remote_workspace_service.go:174`), explicitly exclude `kasmvnc` from the list of protocols accepted for a remote workspace: `slices.Contains(params.Protocols(), e.Name) && e.Name != "kasmvnc"`, with an explicit error message ("kasmvnc is not supported for remote workspaces").
2. Apply the same exclusion everywhere a remote-workspace protocol is validated at connect time (`in.Protocol`, cf. the block around `remote_workspace_service.go:396-399` that calls `params.ValidateTemplateParams`) — check whether there's a single entry point or whether the guard needs to be duplicated; if you duplicate it, factor it into a small function rather than copying the condition.
3. Add a test (`remote_workspace_service_test.go`): registering a remote workspace with `protocol: "kasmvnc"` must fail with 400.
4. The error message propagates as-is to the frontend (`apierror.BadRequest`) — no need to filter on the UI side too, unless you find that a form already offers kasmvnc in a remote-workspace select (check `RemoteWorkspaceDialog.tsx`); if so, remove the option from the list rather than letting the user hit a 400 afterward.

Effort: small (~1h with tests), backend only, zero contract change for remote VNC/RDP/SSH.

---

## Task B: hide the clipboard overlay on kasmvnc sessions

**Gap #1, the most severe in the study.** `SessionOverlay.tsx` (the "clipboard (live)" section, around line 244) renders the copy/paste toggles and the manual exchange for every session with no distinction by protocol. On a kasmvnc session, these controls are silent no-ops: `tunnelRef.current` is null on the kasm branch (`DesktopPane.tsx:95` + kasmvnc branch `:149`), `sendClipboardRef` (`:59`) is never reassigned outside the guac path (only `:231`), and the real clipboard lives inside the KasmVNC iframe, out of the policy's reach (`wwt/internal/kasm/kasm.go`, pure reverse-proxy with no inspection). This is the opposite of the accepted v1 decision ("honest display", `docs/studies/kasm-images-feasibility.md:86`).

**To do**: the `protocol` variable already exists at the component level (`const protocol = protoSwitch.active`, `SessionOverlay.tsx:105`) — use it to gate rendering of the clipboard section (the block around line 244): if `protocol === 'kasmvnc'`, render neither the copy/paste toggles nor the manual exchange block. Replace with either nothing (section absent) or a short text explaining that this session's clipboard is managed by the KasmVNC client itself, outside the WaaS policy — pick the first option if you don't have a new i18n key to justify for a message that only serves a single protocol, the second if you think a user would be surprised by the total absence of the section.

**Points of attention**:
- Don't touch `hasClipboardApi()` or the rest of the overlay (pause/wake/resize/params) — only the clipboard section.
- If `capabilities.clipboardCopy`/`clipboardPaste` stay `true` for a kasmvnc session (the policy can grant a clipboard right on a protocol that can't enforce it), don't silently fix that in this prompt — document it as an additional gap in the commit.
- Add a component test (`SessionOverlay.test.tsx` if it exists, otherwise create it alongside following the convention of the other component tests): a kasmvnc session doesn't render the clipboard controls, a vnc/rdp/ssh session does.

Effort: small to medium (~2-3h with test), frontend only, zero backend contract change.

---

## Task C: real VNC audio via PulseAudio (independent of xrdp)

**Gap #3 (VNC part).** `enable-audio` (`operator/pkg/params/params.go:116`, `Protocols: []string{"vnc"}`, `Tier: ui`) and `audio-servername` (`:121`, advanced) already exist in the registry, with an honest description ("requires the image to run one"). The internal catalog (`waas-images/`) doesn't provide one today (`waas-images/HARDENING.md:80-82`). **Unlike RDP clipboard/audio (task D), this doesn't depend on xrdp/chansrv**: guacd knows how to stream a VNC session's audio directly from a network-reachable PulseAudio server (`audio-servername` parameter), without going through xrdp. This is a standard Guacamole integration, not an extrapolation.

**To do, in `waas-images/base/ubuntu/`**:
1. Install `pulseaudio` (+ `pulseaudio-utils` if needed for smoke tests) in the `Dockerfile`, next to the `tigervnc-standalone-server` block (line ~45-59). Stay within the `--no-install-recommends` principle already in place.
2. Configure PulseAudio to run **in user mode, no root, no setuid** — this is modern PulseAudio's native mode, consistent with the rest of the image (no regression expected on the hardening). Load the network module needed so guacd (which runs in a different pod) can connect over TCP to this pod's PulseAudio server (`module-native-protocol-tcp`, with an ACL/auth consistent with the already-documented threat model — VNC/RDP traffic is already cleartext intra-cluster by design, `HARDENING.md` §Threat model — stick to the same principle rather than inventing a separate network auth mechanism).
3. Supervise PulseAudio via the same mechanism as Xvnc/xrdp (`supervisord`, fragment rendered by `waas-entrypoint` in `${RUNDIR}/supervisor.d/`, cf. the existing pattern for xrdp around `waas-entrypoint:154`).
4. Open the chosen PulseAudio port in `EXPOSE` (`Dockerfile:98`) and in `waas-images/examples/networkpolicy-workspaces.yaml` (add a `port:` entry next to 5901/3389, same `from: guacd`).
5. Bump `version` in `waas-images/base/ubuntu/manifest.yaml` (currently `1.2.0` — this is an image change, tags are immutable, the CI enforces it) and in `waas-images/desktop/xfce/manifest.yaml` if that image derives from the modified base (check `from: ubuntu-base-rdp`).
6. Update `waas-images/HARDENING.md`: remove or rephrase the "Audio is not shipped" line (the "Known, accepted gaps" section) to keep only what remains true (RDP still has no audio, cf. task D), and add an entry under "Enforced at runtime" describing the new PulseAudio component and its network scope.
7. Extend `waas-images/ci/smoke_test.sh` (or the manifests' `smoke:` mechanism) if it's realistic to verify that the PulseAudio module is listening, without requiring a real end-to-end audio stream (out of scope: testing guacd itself).

**Non-negotiable constraint**: don't touch anything in `operator/pkg/params/params.go` — the `enable-audio`/`audio-servername` parameter already exists with the right semantics, this task is an image implementation, not a contract change. Don't go near xrdp/RDP in this task — that's task D's scope, with a different security constraint (see below).

Effort: medium (~1 day), confined to `waas-images/`.

---

## Task D: RDP clipboard + audio — investigate before any code

**Gaps #1 (matrix, line 1) and #3 (RDP part).** Both RDP clipboard (`disable-copy`/`disable-paste` already in the registry, governed by the policy) and RDP audio (`disable-audio`, `enable-audio-input`, `params.go:160,165`) go through **chansrv**, xrdp's channel that carries clipboard and audio. Today, `waas-images/base/ubuntu/Dockerfile:13-19` documents a deliberate decision: xrdp runs **without sesman/PAM**, as a direct bridge (`libvnc` backend) to the already-running Xvnc session — precisely to stay "fully non-root (no PAM, no setuid)", consistent with the rest of the hardening (`HARDENING.md`: zero setuid binaries, CI-verified, `find / -xdev -perm /6000`). chansrv is only started by sesman when a session opens — without sesman, no chansrv, hence no RDP clipboard nor audio. This is the same architectural constraint for both features: don't run two separate investigations, one is enough.

**What's being asked of you, in this strict order**:

1. **Investigate first, without writing any image code.** Concretely answer:
   - Is it possible to run `xrdp-sesman` for a **single, fixed** user session (the container has only one user, `WAAS_USER` UID 1000 — no multi-user switching to handle) without sesman needing real PAM authentication (system password, `/etc/shadow`)? The session password already exists and is verified elsewhere (bridge `password=ask` documented in `waas-entrypoint`) — sesman doesn't need to re-verify a system password, just authorize the container's single user to start their existing session. A PAM module like `pam_permit.so` (accepts any attempt) is the obvious route, but check that it doesn't reintroduce an authentication vector nothing else guards (for example: once PAM is bypassed, does sesman expose a way for any process in the pod to impersonate any user? Here there's only one user, so the impact should be nil, but verify it explicitly).
   - Do the `xrdp-sesman`/`xrdp` packages (and thus chansrv) ship setuid/setgid binaries on Ubuntu 24.04? If so, is that strictly necessary for the targeted single-user mode, or is it tied to xrdp's classic multi-user switching (setuid to change UID at session open) that this deployment doesn't use? The CI smoke test (`ci/smoke_test.sh`) already does `find / -xdev -perm /6000` — any regression will be caught, but do this check yourself before proposing the change.
   - Does sesman need to run as root for its own operation (independently of the need to switch UID), or can it run under UID 1000 like the rest of the image?
2. **If the answer to all three points is favorable** (single-user possible without real PAM, no new setuid required or strippable without breaking function, no root needed): implement it. Add sesman (minimal mode) + chansrv to the `ubuntu-base-rdp` Dockerfile variant, adapt `waas-entrypoint`/`xrdp.ini.tpl`/supervisord accordingly (same pattern as the existing config rendering for xrdp), run the smoke test with `--read-only --cap-drop ALL --security-opt no-new-privileges` to confirm no hardening regression appears, bump the affected manifest versions, and document the new component in `HARDENING.md` (remove the "RDP path has no chansrv" line from "Known, accepted gaps", add a description of the sesman-lite mechanism under "Enforced at runtime", with the security reasoning you validated in step 1). Add an RDP clipboard test in `wwt/internal/guac/clipboard_test.go` if the protocol's wwt-observable behavior changes.
3. **If one of the three points is unfavorable** (unstrippable setuid required, root needed, or an unmanaged real authentication risk): **implement nothing**. Document in `waas-images/HARDENING.md` ("Known, accepted gaps" section) exactly what you found and why it stayed out of reach without regressing the hardening — replace the current line with a version that shows the question was dug into, not just repeated. Don't touch any Dockerfile in this case.

**Don't mix this task with task C**: VNC audio (task C) doesn't depend on any of these answers and must ship regardless of this investigation's outcome.

Effort: variable — the investigation alone is on the order of a few hours; the implementation (if it happens) is a medium-to-heavy effort (~1-2 days), and touches the most sensitive security surface in this entire prompt — don't merge without going through `/security-review` on the final diff.

---

## Task E (the heaviest): real end-to-end dynamic VNC/RDP resize

**Gap #2, but deeper than what the study documents on the surface.** `resize-method` (RDP, `params.go:145`) and the fact that nothing ever resizes the ongoing session are NOT fixed by wiring up `guacamole-common-js`'s `client.sendSize()`: the script already present in the image, `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-resize`, explicitly documents that **the xrdp-libvnc bridge can't propagate a resize to the underlying Xvnc anyway**, even if guacd received the instruction. Sending `sendSize()` would therefore be a round trip for nothing. The only path that actually works today is this `waas-resize` script executed **inside the pod**, which drives Xvnc via `xrandr` (RandR `SetDesktopSize`, natively supported by TigerVNC) — nothing currently calls it from the outside.

The mechanism to build is therefore **WaaS-specific, not guacd's native resize mechanism**: desktop resized by running a command inside the pod, independently of what RDP/VNC natively know how to do. This applies to VNC AND RDP (both run on the same Xvnc in this image catalog) — not to kasmvnc (already handled natively client-side, `DesktopPane.tsx:156-163`) nor SSH (no desktop).

**Recommended architecture** (verified against the existing code, not a guess):

- **wwt today has no cluster access at all** (no Kubernetes client, RBAC, or k8s dependency in `wwt/`) and its current architecture ("talks only to `shared/auth` + the internal API") should not gain one for this need — it's not its responsibility.
- **`api-server` already has a Kubernetes client and RBAC access** (`s.kube`, controller-runtime client, used everywhere in `WorkspaceService`, e.g. `Reload` at `workspace_service.go:497`). So it's `api-server` that must execute the command inside the pod, not wwt.
- The frontend already talks directly to `api-server` for session actions (pause/wake/reload) without going through wwt — a new public endpoint follows the same path, no need to invent a channel through the guac tunnel (which is an opaque binary stream, a poor candidate for carrying an out-of-band signal).

**To do**:

1. **Frontend**: in `DesktopPane.tsx`, the existing `ResizeObserver` (line ~292) today only rescales the canvas in CSS. Add, **debounced** (a browser window resize triggers dozens of events per second — aim for ~500ms after the last change before acting), a call to a new endpoint when the session's protocol is `vnc` or `rdp` (not kasmvnc, not ssh — check the active protocol the same way `SessionOverlay.tsx:105` already does).
2. **api-server, new public endpoint** (next to the other existing session actions, e.g. `Reload`/`Connect` in `workspace_service.go` + the corresponding handler): `POST /api/v1/workspaces/{id}/resize` (name to adapt to the REST convention already in place — look at how `Reload` is exposed to stay consistent), body `{width, height}`.
   - Reuse the existing authorization (`fetchByID(ctx, actor, id)`, same model as `Reload`) — no new auth logic.
   - Explicitly reject remote workspaces (`kind: "remote"`): they have **no pod at all** (`model.go:82-86`, "no template, no operator lifecycle, no compute") — an explicit 400/409, not a silent failure.
   - Strictly validate `width`/`height` server-side **before** building anything (regex or numeric bounds, e.g. 100-7680 on each axis) — defense in depth even though `remotecommand` doesn't invoke a shell (no injection possible via the arguments, but absurd values could still do anything to `xrandr` inside the pod).
   - Resolve the target pod by label selector in the workspace's namespace (`waas.xorhub.io/workspace=<name>`, cf. `operator/pkg/metakeys` for the exact key and `operator/pkg/naming` for the naming convention) — no pod resolution exists in `api-server` today, you're the first to write one; keep it minimal (a single pod expected per workspace).
   - Execute `waas-resize WIDTHxHEIGHT` via `client-go` (`kubernetes.Interface.CoreV1().Pods(ns).GetLogs`/`RESTClient().Post().Resource("pods").SubResource("exec")` + `remotecommand.NewSPDYExecutor`) — **fixed command, a single argument built from already-validated integers, never a shell** (no `sh -c`). `s.kube` (controller-runtime client) is not enough for exec — you additionally need a `kubernetes.Interface` (classic client-go) and a `*rest.Config`; look at how `main.go` already builds the in-cluster config for the rest and add what's missing to the service's constructor, without duplicating the connection config.
   - If the workspace isn't `Running`, return an explicit conflict (same pattern as `Reload`, `workspace_service.go:502-504`).
3. **RBAC** (`helm/waas/templates/api-server.yaml`): the api-server's `ClusterRole` today has `pods: [get, list]` (around line 73-75) — add a separate entry `resources: [pods/exec]`, `verbs: [create]`. **Don't mix this verb into the existing `pods` entry**: `pods/exec` is a full sub-right of its own and deserves its own visible line in review, not buried in the `get, list` for the rest.
4. **Tests**: on the Go side, test at least the width/height bounds validation and the explicit rejection of remote workspaces and non-Running workspaces (a fake client is enough, no need for a real exec in tests — mock/interface the executor so as not to depend on a real pod). On the frontend side, test that the debounce doesn't send a request per pixel and that kasmvnc/ssh never call the endpoint.
5. **Documentation**: add a note in `docs/` (the closest file is probably `docs/diagnostics/` or a new short doc) explaining that this resize is a homegrown WaaS mechanism (direct exec inside the pod), not RDP/VNC's native resize — so the next reader doesn't go looking for `sendSize()` in the guac tunnel and waste time the way this study nearly did.

**What stays out of this task's scope**: making `resize-method` (the guacd RDP parameter) itself functional in the proper sense of the term — this parameter would remain inert in the classic guacd/RDP sense even after this task, since the real mechanism completely bypasses guacd. If you judge that this makes the parameter misleading once this mechanism is in place (the user gets a real resize, but not via the path `resize-method` is supposed to control), flag it at the end of the task rather than fixing it yourself — this is a product decision (should `resize-method` be kept/removed/reworded now that the real mechanism lives elsewhere), not a technical bug.

**Security — don't merge without a pass on this**: this task adds a new command-execution capability inside a pod, triggerable from the browser. The blast radius is deliberately narrow (a fixed command, two validated integers, a binary that additionally validates its own format), but `pods/exec` is a significant RBAC right — run the final diff through `/security-review` before considering it done, independently of the unit tests.

Effort: heavy (~2-3 days), spans frontend + api-server + Helm RBAC + docs — the most cross-cutting task in this prompt.

---

## Task F: empty kasmvnc forms — no action

**Gap #6.** `ForProtocol("kasmvnc")` returns no parameters by construction (`params.go:416-434`): the portal's "protocol parameters" tabs will never show anything for kasmvnc, real configuration going through `kasmvncConfig` (opaque, admin-only YAML). This is a state judged correct — **do nothing here**, don't fabricate a message specific to a single protocol to fill a "gap" that isn't one. Mentioned only so you don't rediscover it as a bug along the way.

---

## Cross-cutting constraints

- `tsc -b` with no error, `strict: true`, zero `any` on every frontend change.
- `go build ./...` + existing Go tests green on every backend change.
- i18n: every new string goes through `frontend/src/i18n/locales/{en,fr}.json` — but think before adding one for a single protocol (task B): prefer no text over a message that only exists to justify its own existence.
- Don't extend the `pkg/params` registry in this prompt (none of the tasks require it — C/D change the image behind an already-existing parameter, not the parameter itself).
- Each task is shippable on its own — don't wait to have done A→E before committing the first one.
- Tasks C, D, and E touch either the image catalog's security or a new cluster execution capability: never consider them "done" in the sense of this prompt until the hardening smoke test (`ci/smoke_test.sh`, `--read-only --cap-drop ALL --security-opt no-new-privileges`) and, for E, a `/security-review` pass, have confirmed there's no regression.

## Open points (your call, summary)

- Task B: absent clipboard section vs. explanatory message for a kasmvnc session.
- Task D: implement or document-only depending on what the sesman/PAM investigation actually reveals — don't force an implementation if one of the three security criteria isn't clearly met.
- Task E: what to do with `resize-method` once the real mechanism lives elsewhere — flag it, don't decide silently.
