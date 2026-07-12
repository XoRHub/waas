# Fable 5 Prompt — Feature 6: expose the audio port (PulseAudio) when server-audio is enabled

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

Commit `61c8b29d184a feat(images): VNC audio via a non-privileged PulseAudio` added server-side audio support for VNC sessions. The guacd parameter `enable-audio` (`operator/pkg/params/params.go:116`, bool, VNC, `TierUI`) and `audio-servername` (line 121, string, VNC, `TierAdvanced`) already exist in the registry — but **nothing opens the network port that PulseAudio listens on**, neither on the Pod side (`ContainerPort`) nor on the Kubernetes `Service` side, nor in the UI, nor in the CR. So the guacd parameter can be enabled without the associated application port ever being reachable.

## What already exists (know this before coding)

**The PulseAudio port is fixed, wired into the image, never exposed on the cluster side**:
- `waas-images/base/ubuntu/rootfs/etc/waas/pulse/default.pa.tpl:16`: `load-module module-native-protocol-tcp port=4713 auth-anonymous=1` — port **4713**, hardcoded, not configurable via an environment variable.
- `waas-images/base/ubuntu/Dockerfile:23` (`EXPOSE ... 4713`) and `:120` (`WAAS_AUDIO_ENABLED=1`): the whole PulseAudio stack is on/off via `WAAS_AUDIO_ENABLED`, but `EXPOSE` in a Dockerfile is only documentation — it opens nothing on the Kubernetes side.
- `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-entrypoint:98-121`: starts the PulseAudio supervisord program only if audio is enabled.

**The CRD has only a single port per protocol, no list**: `WorkspaceProtocol` (`operator/api/v1alpha1/workspacetemplate_types.go:198-242`) has exactly one `Port int32` field (lines 210-213). No `extraPorts`/`additionalPorts` field exists anywhere in `workspace_types.go` or `workspacetemplate_types.go`.

**The operator builds ports 1:1 from this single value**:
- Container: `operator/internal/controller/workload.go:69-73` — `ports = append(ports, corev1.ContainerPort{Name: p.Name, ContainerPort: p.Port})` for each protocol in `EffectiveProtocols()`. Port 4713 is never added.
- Service: `operator/internal/controller/workspace_controller.go:813-821` (`ensureService`) — same logic, one `ServicePort` per declared protocol, same source.

**Critical point to know before touching the Service**: `ensureService` **only creates the `Service` once** — `if err == nil { return nil }` (`workspace_controller.go:806-808`): if the Service already exists, it is **never updated**, even if the port list changes afterward. This is consistent with a gap already documented by the audit regarding `podTemplate` (`create-only`, `docs/studies/audit-2026-07.md` §Operator) — but for this feature, it means adding the audio port in the code will not be enough to make it appear on an **already existing** workspace unless the create-only Service is also fixed (at least for ports), otherwise the admin will have to recreate the workspace for the port to appear.

**Dev environment**: `hack/dev/templates-dev.yaml` doesn't reference audio anywhere; all templates have only one port per protocol (`vnc:5901`, `rdp:3389`, `ssh:2222`, `kasmvnc:6901`).

**Smoke tests**: `test/smoke/smoke_test.go` (`TestProtocolConnections`, ~line 47) drives `WAAS_SMOKE_PROTOCOLS` (default `vnc,rdp,ssh,kasmvnc`), one `t.Run(protocol, ...)` sub-test per protocol via `connectOnce` (~lines 78-128): create → poll Running → `POST .../connect` → guacd WS establishment. This is the skeleton to extend, not a new pattern to invent.

## What needs to be delivered

### A. Expose the port in the CRD, the operator, the Service

Add the notion of an auxiliary port on the `WorkspaceProtocol` side (`workspacetemplate_types.go`) — recommendation: a simple field tied to audio rather than a generic `extraPorts[]` mechanism (the PulseAudio port is fixed at 4713, not configurable; over-generalizing now for a single use case isn't justified — see "Open points" if you identify a real need for genericity). Wire this field into:
- `workload.go:69-73`: add the `ContainerPort` 4713 when the audio port is requested for the VNC protocol.
- `ensureService` (`workspace_controller.go:813-821`): add the corresponding `ServicePort`, **and fix the create-only issue at least for port convergence** (an existing `Service` must have its `Spec.Ports` updated if the expected list changes) — otherwise this feature will never work on a workspace already provisioned before audio was enabled.

### B. UI: the port-add menu appears when `enable-audio` is turned on

`ParamField.tsx`/`ProtocolTabs.tsx` today render params generically, with no cross-field conditional logic (no UI shows/hides a field based on the value of another). Add this conditional behavior: when `enable-audio` is set to `true` in the form (`CreateWorkspaceDialog`, `TemplatesPage`, `ConnectionSettingsDialog` — all consume `ProtocolParamsForm`), show an explicit section/checkbox "Expose the audio port (4713)" that drives the new CRD field. Document in `ParamField.tsx`/`ProtocolTabs.tsx` (comment) this first case of conditional rendering, keeping in mind that other upcoming features may want to group/link fields together (see Feature 7 of this series) — don't build a generic inter-field dependency mechanism if a simple `enable-audio && <Port field />` suffices here.

### C. DEV environment: a dedicated CR + smoke test

- Add to `hack/dev/templates-dev.yaml` a template (or a variant of an existing VNC template) with `enable-audio: true` and the port exposed, so it can be tested manually in dev.
- Extend `test/smoke/`: a sub-test that, for an audio-enabled template, checks that port 4713 is actually reachable after connecting (direct TCP dial, or via a minimal PulseAudio client like `pactl`) — same structure as the existing protocol sub-tests in `connectOnce`.

## Constraints to respect

- Don't widen the port's exposure beyond the cluster: like other session ports, it must only be reachable internally (ClusterIP Service), never via the public Ingress (`helm/waas/templates/ingress.yaml`/`httproute.yaml` only allow-list explicit paths — don't touch them).
- The Service create-only fix must stay scoped to port convergence for this feature — don't launch into a general `podTemplate` convergence refactor (a gap already documented separately by the audit, out of scope here).
- Go tests on the new CRD field (webhook validation if applicable), on `workload.go`/`ensureService`. Vitest test on the new conditional rendering of the port field in `ParamField.tsx`/`ProtocolTabs.tsx`.
- i18n for any new UI string.

## Open points (your call)

- Dedicated CRD field (`AudioPort`/boolean "expose the audio port") vs a generic reusable `extraPorts []PortSpec` mechanism for future similar needs — the recommendation above leans toward the dedicated field (YAGNI), to be revisited only if you see a second concrete use case in the repo that would justify generalization.
- Scope of the "Service create-only" fix: fixing it only for ports (minimal, scope of this feature) vs for the whole `Spec` (the real undertaking already identified by the audit, riskier and out of scope) — stick to the minimal fix unless you judge that a partial fix would introduce an inconsistency worse than the status quo.
