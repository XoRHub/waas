# Accepted limitations (API-yes, portal-no)

The portal is a **mirror** of server-side rights, never the enforcement
point: the admission webhook judges every request identically whether it
comes from the portal, `curl` or `kubectl`. A few delegated override
rights deliberately have **no portal UI**. This page records each gap,
why it is accepted, what it implies for users, and the supported bypass
— so the omission reads as a decision, not an oversight.

Each entry names its **revisit trigger**: the concrete signal that
should reopen the decision. Until a trigger fires, do not build the UI.

The end-user version of this page (with full walkthroughs) lives on the
website: `docs/accepted-limitations.md` in the `website` repo. Keep the
two in sync when an entry changes.

## 1. Advanced overrides: `securityContext` / `podSecurityContext` / `volumes`

**State.** `WorkspaceOverrides` carries these fields
(`operator/api/v1alpha1/workspace_types.go`), templates can delegate
them (`overrides.allowedFields`), the creation API accepts them verbatim
(`CreateWorkspaceInput.Overrides`), the webhook judges them. No portal
editor exists, and they are also absent from the runtime PATCH
(`UpdateOverridesInput`) — they are **creation-time only**.

**Why accepted.** These fields are pod-spec-shaped: a structured form
would either fake safety or reimplement the pod schema. More
importantly, a delegated `securityContext` right covers the *whole*
struct — `privileged: true` included — and today the only users who can
exercise it are those able to write the API call. That friction is a
deliberate part of the risk profile: making the right one click away
changes what "delegating it" means for an admin.

**Implication (example).** On a `dev-tools` template delegating
`securityContext` + `volumes`, a user who needs `SYS_PTRACE` for
`strace`/`gdb` plus a scratch volume cannot get them from the portal —
only via the API (see the website page for the full `curl`/CR example).

**Semantics to keep in mind** (source of subtle bugs if a UI ever
comes): `securityContext`/`podSecurityContext` **replace** the
template's; `volumes`/`volumeMounts` **append** (same name wins).

**Revisit trigger.** A real persona asks for it repeatedly. If built:
a collapsed YAML section in the creation dialog (reuse `YamlEditor`,
symmetric to the admin `WorkloadSection`), gated on the rights — **and**
extend `UpdateOverridesInput` in the same change, or we recreate the
`metadata` dead-end this repo just fixed.

## 2. `targetNamespace` at creation (the `placement` right)

**State.** `CreateWorkspaceInput.TargetNamespace` overrides the
template's placement pattern (frozen into `spec.targetNamespace`);
needs the `placement` right; the portal always uses the resolved
pattern and only shows the preview.

**Why accepted.** A free-text namespace field is the wrong UI: namespace
creation is **create-only bootstrap** (`ensureNamespace`,
`operator/internal/controller/placement.go`), so a typo does not fail —
it *creates* a fully bootstrapped namespace (quota, NetworkPolicy, PSA
labels). Silent sprawl. Doing it properly needs the admission logic to
become *enumerable* (a `GET /me/placement-targets` style endpoint
feeding a select), which is real backend work with no current consumer.

**Implication (example).** A team lead with the `placement` right who
wants a workspace in the shared `waas-team-blue` namespace (common
quota, team NetworkPolicy) must pass `targetNamespace` through the API.

**Revisit trigger.** An actual multi-tenant scenario where teams share
namespaces. Build the enumeration endpoint first, never free text.

## 3. Metadata overrides display the override, not the merged result

**State.** The runtime form and creation dialog show the user's
`labels`/`annotations` override verbatim. The workload carries the
merge (`workloadMeta`, `operator/internal/controller/workload.go`):
override merged **under** template metadata, operator/platform keys
always win, reserved domains rejected by `pkg/metakeys`.

**Why accepted.** Showing the merged result would mean either
duplicating `workloadMeta` logic in the api-server (or extracting a
shared package), or reading workloads from the api-server — the latter
crosses an architecture boundary on purpose kept closed. The collision
case (an override key shadowed by a template key) is marginal, and the
UI hint states that platform keys win.

**Implication (example).** Template sets `team: platform` on the
workload; a user override sets `team: blue`. The form shows `blue`,
`kubectl get deploy` shows `platform`. The truth is always on the
workload object, in the namespace/workloadName the workspace card
displays.

**Revisit trigger.** Support sees actual confusion. Then: extract
`workloadMeta` into a shared package and expose an `effective*`
projection on the model — never read workloads from the api-server.
