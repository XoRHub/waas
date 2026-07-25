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

**What delegating these rights actually grants.** The allow-list gates
the *field*, never its *content* — there is no platform-side validation
of what goes in it, by design (below). Read literally:

- `volumes` accepts any `corev1.VolumeSource`. A `secret:` entry mounts
  **any Secret co-located in the workspace namespace** — at minimum the
  registry pull secret and the per-workspace SSH key. `projected:` can
  additionally re-introduce a `serviceAccountToken` the platform
  deliberately turns off (`AutomountServiceAccountToken: false`).
  `csi`, `cephfs`, `rbd`, `iscsi` and friends carry their own
  `secretRef`, so they are the same primitive by another route.
- `securityContext` covers the whole struct, `privileged: true`
  included. Only the namespace's Pod Security Admission level stops it.

**Why WaaS does not validate this content.** Pod Security Admission does
**not** cover it: `restricted` explicitly permits `secret` and
`projected` volumes, because mounting a Secret from your own namespace is
normal Kubernetes. Reimplementing that judgement inside WaaS would mean
maintaining a parallel policy engine over a `VolumeSource` union that
gains fields every Kubernetes release — and it would sit in the wrong
place. Constraining *how* a delegated pod-spec field may be used is a
cluster-administration decision, expressed with the tools built for it:
**ValidatingAdmissionPolicy** (in-tree since 1.30, CEL, no dependency),
Kyverno or Gatekeeper, scoped to the workspace namespaces.

So the rule is: **delegate these rights only to principals you would
trust with the namespace itself**, and pair the delegation with an
admission policy if that trust is partial. The bootstrap default policy
does not grant them; the shipped `gitops/governance/policies.yaml`
standard-user policy does not either. (`volumes` used to be in the
bootstrap default's allow-list; deployments that relied on that grant
now re-add it explicitly via `defaultPolicy.overrides.allowedFields`.)
Granting them is an explicit, auditable act.

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

## 4. Usernames must stay distinct once projected into a DNS label

**State.** The built-in placement default gives each user their own
namespace, named from `naming.Sanitize(username)` — a lossy projection:
`alice.smith`, `alice_smith`, `Alice Smith` and `ALICE.SMITH` all become
`alice-smith`. The database's `UNIQUE` constraint is on the raw column,
so such accounts are distinct to the platform and would share one
namespace, one ownership label and one ResourceQuota. WaaS refuses the
second account instead of disambiguating it: `409` at creation
(`UserService.Create`), and a failed login with an audited
`user.sso_placement_conflict` at first SSO provisioning
(`OIDCService.resolveUser`).

**Why accepted.** Directories already number their homonyms (`jdoe`,
`jdoe2`) — rewriting that convention is not the platform's job, and a
generated discriminator would put a hash in every namespace name to
serve a case that a well-formed directory does not produce. Refusing
keeps the names readable and makes the anomaly visible where it can be
corrected.

**It is an identity rule, not a placement one.** The check runs whatever
the placement pattern resolves to, including a deployment that opted
into a shared namespace with a token-less literal: two accounts whose
names are indistinguishable in every DNS-derived artifact are a
directory defect on their own, and a template added later can put
`{user}` back in a pattern at any time. For local accounts the platform
owns the namespace of names and enforces it outright. For SSO it does
not: the directory is the authority, so the fix belongs there — WaaS
only has to refuse clearly and say why in the audit trail.

**Not the same as an unrepresentable username.** A Cyrillic, CJK, Greek
or Arabic username leaves *no* DNS-1123 character at all, so every such
account would project onto the same label. Those are **not** refused:
the `{user}` token falls back on the first and last groups of the
account id (`waas-a1b2c3d4-ef1234567890`, `naming.IdentitySegment`),
which is unique per account and, unlike a hash, lets an admin find the
owner with a prefix query. The refusal above only concerns usernames
that *do* produce a label and produce the *same* one.

**Implication (example).** An admin creates `alice-smith` locally,
Authentik later provisions `alice.smith` for the same person: the second
login fails with *"SSO login failed for this account — contact an
administrator"*. The user-facing message stays generic on purpose — the
caller is unauthenticated and the detail would disclose another
account's username. The admin finds it in the audit trail: `username
"alice.smith" normalizes to "alice-smith", already used by account
"alice-smith"`. Remedy: rename one account in the directory, or delete
the stale local one.

**Not enforced by the database.** Both checks read the username column
and then insert; the `UNIQUE` constraint is on the raw value and cannot
see the projection. Two colliding accounts created concurrently — two
`POST /api/v1/users`, or two first SSO logins — can therefore both land,
and nothing re-checks afterwards. That needs the square of two rare
events (a name collision *and* simultaneity), so it is accepted rather
than paid for with a maintained normalized column and a unique index,
which is the fix if it ever happens.

**Revisit trigger.** A directory that cannot be changed hits this.
Then: a configurable **alias claim** on the SSO token, consulted as the
placement name when the username collides — the fallback that keeps the
refusal from being a dead end, without WaaS inventing names.
