# WaaS Security Audit & Penetration Test — 2026-07-20

Dedicated **security** audit of the WaaS platform (operator, api-server,
wwt tunnel, frontend, Helm chart, K8s/RBAC, OIDC). This is distinct from
the 2026-07-08 quality audit (`audit-2026-07.md`): here every finding is a
potential *exploitable* weakness, reasoned against an explicit threat
model, then adversarially verified.

## Remediation status

This report is a **point-in-time record of 2026-07-20**, kept as written.
The findings below are not a list of open weaknesses — most are closed.
Current state:

| # | Severity | Status |
|---|---|---|
| 1 | High | **Fixed** |
| 2 | Medium | **Fixed** — per-request revocation + server-side logout |
| 3 | Medium | **Fixed** — override `env` is literal-only |
| 4 | Medium | **Fixed** — dedicated short-lived SSE stream token |
| 5 | Medium | **Fixed** — default-deny egress on desktop namespaces |
| 6 | Low | **Fixed** — query-string auth scoped to the SSE stream |
| 7 | Low | **Mitigated** — in-cluster targets refused at create, update and connect; a guardrail, not containment (see the note below) |
| 8 | Low | **Fixed** — with 5 |
| 9 | Low | **Closed** — cluster-admin arbitration, default tightened (see the note below) |
| 10 | Low | **Fixed** — desktop pods no longer mount the SA token |
| 11 | Low | **Closed** — cluster-admin arbitration; catalog measured compliant with `restricted` (see the note below) |
| 12 | Low | Deferred — prerequisite for taking KasmVNC out of experimental; residual documented in `docs/kasmvnc.md` |
| 13 | Low | **Fixed** — session moved to an httpOnly cookie; the SPA stores no credential |
| 14 | Low | **Won't fix** — HSTS belongs to the operator's ingress / reverse proxy |
| 15 | Low | **Fixed** — `postgres.sslMode` is a chart value |

Anything below describing a weakness marked *Fixed* describes the code as
it stood on 2026-07-20, not as it stands today.

## Method

- **Static** — a fleet of six independent auditors (one per security
  domain: authn/authz, API access-control & injection, operator/K8s,
  wwt tunnel, frontend, deploy/supply-chain). Every raw finding was then
  handed to a second, adversarial verifier tasked to *refute* it by
  re-reading the cited code. 17 raw → **16 retained, 1 refuted**.
- **Dynamic (live pentest)** — a throwaway `k3d` dev cluster
  (`make dev-bootstrap`, zero impact on the home-lab) driven through the
  real portal API with the seeded governed accounts
  (`admin`/`admin123`, `dev`/`dev123` power-user, `user`/`user123`
  default policy). Attacks actually executed, not just reasoned.
- Nothing was run against the live `waas.internal.drummy.cloud` /
  home-lab cluster.

Threat model: an authenticated non-admin tenant escalating (IDOR,
privilege, quota, tenant/namespace escape, SSRF, secret theft); an
unauthenticated attacker on public endpoints; a malicious/replayed OIDC
flow; a tenant with a shell in their own desktop pod pivoting to the
platform.

## Executive summary

The platform's **core security design is strong and largely held up under
live attack.** Authentication, authorization/IDOR, injection, and the
tunnel's confused-deputy defenses are genuinely well-built — see
[Positive assurances](#positive-assurances-verified). The residual risk
is concentrated in **availability** (one confirmed remote-crash DoS) and
in **tenant-pod isolation hardening** (egress, service-account tokens,
override content-validation, session revocation).

| # | Severity | Finding | Confirmed how |
|---|----------|---------|---------------|
| 1 | **High** | Malformed Guacamole frame crashes the shared `wwt` process → cross-tenant DoS | static + code repro |
| 2 | **Medium** | No token/session revocation — deactivation & role-demotion ineffective for up to 8 h | static + live (8 h TTL, no refresh cookie) |
| 3 | **Medium** | `env` override renders tenant-controlled `valueFrom.secretKeyRef` verbatim into the pod → exfil of any Secret in the pod's namespace | **live-confirmed** (operator wrote `secretKeyRef: waas-postgres` into the Deployment podTemplate) |
| 4 | **Medium** | 8 h platform bearer sent in the SSE URL query string (logs/history/Referer exposure) | static + live |
| 5 | **Medium** | Desktop pods have **no egress** NetworkPolicy → IMDS / kube-API / lateral pivot | static |
| 6 | Low | `?access_token=` accepted on *every* authenticated route, not only SSE | static |
| 7 | Low | Remote-workspace hostname unrestricted → in-cluster SSRF (feature is admin-gated, no telnet) | static + live (internal targets reach validation) |
| 8 | Low | Placed-namespace NetworkPolicy is ingress-only (egress unrestricted) | static |
| 9 | Low | `securityContext`/`volumes` overrides have no *content* validation (needs admin grant; baseline PSA backstops) | static |
| 10 | Low | Desktop pods auto-mount the namespace default SA token | static |
| 11 | Low | Placed namespaces enforce PSA `baseline` (not `restricted`), no hardened default container context | static |
| 12 | Low | KasmVNC upstream reverse-proxy uses `InsecureSkipVerify: true` | static (documented residual) |
| 13 | Low | Access token persisted in `localStorage` (XSS ⇒ 8 h token theft; strong CSP mitigates) | static |
| 14 | Low | No HSTS emitted by the chart (relies on ingress-nginx default) | static |
| 15 | Low | Postgres connection string pinned `sslmode=disable` (bundled PG serves no TLS anyway) | static |

*(Findings 10 and 15 in the source run were split across two domains for
the SA-token and sslmode items; consolidated here.)*

**Refuted (1):** "tenant desktops share the platform namespace with no PSA
and no NetworkPolicy" — false. An empty placement pattern resolves to the
operator-created `waas-workspaces` namespace, which is stamped
`enforce=baseline` + a default-deny **ingress** NetworkPolicy. The
desktops do **not** land next to the control plane and secrets.

---

## Detailed findings

### 1 — [High] Malformed Guacamole frame crashes the shared wwt process (cross-tenant DoS)

`wwt/internal/guac/instruction.go:50` — `ReadInstruction` parses the
element length with `strconv.Atoi` and never bounds- or sign-checks it,
then does `make([]rune, 0, length)`.

```
length, err := strconv.Atoi(strings.TrimSuffix(lengthStr, "."))
value := make([]rune, 0, length)     // length attacker-controlled
```

Attack: any authenticated tenant with a valid guacd-protocol connection
JWT opens `GET /ws?token=…`. The browser→guacd direction has **no
size-bounded framer** (unlike guacd→browser, which is guarded by
`completePrefix`/`MaxPendingBytes`). A WebSocket text frame beginning
`-1.x,` reaches `ReadInstruction` (it is not a tunnel-internal `0.`
frame, so `IsInternalMessage` is false), yielding `make([]rune, 0, -1)`
→ `panic: makeslice: cap out of range`. The panic fires in the
handler-spawned pipe goroutine (`proxy.go:243`); there is **no
`recover()` anywhere in wwt** (repo-wide grep empty), so the whole `wwt`
process dies, disconnecting **every** concurrent tenant tunnel on that
pod. A large positive length (`2000000000.`) forces an ~8 GB `[]rune`
allocation → OOM-kill, same blast radius. Reproduced on the local Go
toolchain.

**Fix:** in `ReadInstruction`, reject `length < 0 || length > MaxPendingBytes`
before allocating; add `ws.SetReadLimit` on the browser side; wrap the
pipe goroutines in `recover()` so one malformed stream cannot take down
the shared process.

### 2 — [Medium] No token/session revocation (deactivation/demotion ineffective ≤ 8 h)

`api-server/internal/middleware/middleware.go:41` — `Auth` validates the
stateless RS256 JWT (signature/iss/aud/exp) and **never** consults the DB
for the user's current `Active` flag or `Role`. Tokens default to an
**8 h TTL** (`config.go:151`) with **no refresh cookie** (confirmed live:
login returns only `accessToken`+`expiresAt`+`user`, no `Set-Cookie`).

Consequence: an admin who deactivates a compromised user
(`user_service.go` sets `Active=false`), or an OIDC group-sync that
demotes an ex-admin, does **not** cut off the already-issued token — full
prior privilege (including `role:admin`) survives up to 8 h. Only
`/auth/me` re-checks existence.

**Fix:** on the authenticated path re-check `Active`/role against the
store (short cache) or keep a per-user `tokensValidAfter` and reject
older `iat`; shorten the TTL and add a refresh flow (already tracked as
batch 2).

### 3 — [Medium] `env` override → secret exfiltration via `valueFrom.secretKeyRef` — **live-confirmed**

`operator/internal/controller/workload.go:445` (`desktopEnv` →
`mergeEnv`) lays the tenant's override env over the template env **with no
filtering of `valueFrom`**. Neither the api-server (`UpdateOverrides`
does `ov.Env = *in.Env`) nor the validating webhook strips it, and the
CRD schema keeps the full `EnvVar` (`valueFrom`/`secretKeyRef` are not
pruned).

**Live proof** (dev k3d): as `dev` (power-user, `env` override right),
`PATCH /workspaces/{id}/overrides` with

```json
{"env":[{"name":"STEAL_DB","valueFrom":{"secretKeyRef":{"name":"waas-postgres","key":"password"}}}]}
```

→ `200`, persisted verbatim into the Workspace CR
(`spec.overrides.env`), and after a workload rebuild the operator
rendered it straight into the Deployment podTemplate:

```
STEAL_DB -> SECRET:waas-postgres     # tenant-chosen secret name, unfiltered
```

Any pod from that Deployment exposes the referenced secret's value in its
environment, readable by the tenant who has a shell in the desktop.

**Scope / calibration:** `secretKeyRef` resolves in the **pod's own
namespace**, so this reads any Secret co-located with the desktop —
which today means the shared `dev-ssh-credentials`, any operator-copied
pull/credential secrets, and, **if a placement namespace is shared
between tenants, another tenant's per-workspace secret** (name-guessable
`<workspace>`). It does **not** reach the platform `waas` namespace
(JWT signing key, DB creds live there, namespace-isolated) — hence
medium, not critical. `fieldRef`/`configMapKeyRef` are accepted the same
way. (Note also: the PATCH is accepted only while the workspace is
`Pending`/`Provisioning`; on a `Running` workspace it returns 403 — a
timing constraint, not a fix, and the natural schedule scale-up applies
the stored override anyway.)

**Fix:** in the operator (or webhook), restrict override `env` entries to
**literal `value` only** — drop/reject any `valueFrom`. Desktop-namespace
Secrets a tenant legitimately needs are already injected by the operator
(`WAAS_DESKTOP_PASSWORD`/`VNC_PW`) scoped to the workspace's own secret.

### 4 — [Medium] 8 h bearer token in the SSE URL query string

`frontend/src/hooks/useEvents.ts:33` opens
`GET /api/v1/events?access_token=<8h JWT>` (EventSource cannot set
headers). The token then lands in reverse-proxy/ingress access logs and
browser history. **Fix:** mint a separate short-lived, audience-scoped
stream token for SSE instead of reusing the full API access token; and
scope the server-side query fallback to `/events` only (see #6).

### 5 — [Medium] No egress NetworkPolicy on desktop pods

`operator/internal/controller/placement.go:162` — the operator's
per-namespace policy declares `PolicyTypes: [Ingress]` only; **egress is
never restricted** (no egress policy anywhere in `operator/` or `helm/`).
A tenant shell in a desktop pod can reach cloud IMDS
(`169.254.169.254`), `kubernetes.default.svc:443`, the kubelet
(`:10250`), and platform-namespace services. Impact is
environment-conditional (no IMDS on k3d/bare-metal; the mounted SA is
unprivileged — see #10), which is why it is medium not high, but it is
the natural pair to #10. **Fix:** add a default-deny egress policy
allowing only DNS + required platform services, and block the
link-local metadata range.

### 6–15 — Low findings (hardening / defense-in-depth)

> **Maintainer decision, 2026-07-25 — findings 9 and 11 are settled as an
> arbitration OUTSIDE WaaS, not as platform defects.**
>
> Both ask WaaS to judge how a delegated pod-spec-shaped field may be
> used, or which Pod Security Admission level a namespace must enforce.
> Neither belongs to the platform: constraining a delegated pod-spec
> field is what an admission policy engine is for
> (ValidatingAdmissionPolicy, Kyverno, Gatekeeper), and the PSA level of
> a namespace is the cluster administrator's call. Re-implementing either
> inside WaaS would duplicate cluster tooling with a parallel policy
> engine over a `VolumeSource` union that changes every Kubernetes
> release — and would place the decision at the wrong layer.
>
> Note the audit's own mitigation claim for #9 is wrong and must not be
> repeated: PSA does **not** backstop the `volumes` half. `restricted`
> explicitly permits `secret` and `projected` volumes.
>
> What was done instead:
> - **Defaults tightened**: the bootstrap policy no longer grants the
>   `volumes` override to every authenticated user
>   (`helm/waas/values.yaml`, `defaultPolicy.overrides.allowedFields`),
>   aligning the chart with the GitOps reference policy which already
>   omitted it. Granting it becomes an explicit, auditable act.
> - **Documented honestly**: `docs/accepted-limitations.md` §1 now states
>   what delegating `volumes`/`securityContext` actually grants, why WaaS
>   does not validate the content, and which cluster-side tool to pair
>   with the delegation. Mirrored user-side on the website.
> - **#11 measured, then closed the same way.** Against the published
>   catalog (`ubuntu-desktop-noble`, `firefox`, `kasmweb/terminal`), the
>   desktop pod fails `restricted` on exactly three controls —
>   `allowPrivilegeEscalation`, `capabilities.drop=[ALL]`,
>   `seccompProfile=RuntimeDefault`; `runAsNonRoot` is already satisfied.
>   Supplying those three, all three images start and serve normally
>   under `enforce=restricted`, so **nothing in the catalog requires
>   `baseline`** and no image change is needed anywhere.
>
>   WaaS still does **not** set them. Not from caution — from the same
>   rule as #9, with a sharper mechanism: a hardened cluster fills the
>   container `securityContext` with a cluster-wide mutation policy
>   (Kyverno, `MutatingAdmissionPolicy`, Gatekeeper), and those are
>   almost always written *add-if-absent*. An operator-supplied default
>   would silently take desktop pods out of that policy's reach —
>   hardened everywhere except where it matters — and a Helm toggle would
>   not help, since the default behavior would still be the harmful one.
>
>   Delivered instead: the measurement and the procedure to raise the
>   level are documented (`docs/placement.md`), and the code comment that
>   justified `baseline` by a first-boot `chown` needing capabilities —
>   now disproved — was corrected. `warn=restricted` was already in place
>   and remains the canary.
>
> No content-validation code will be written for #9, and no default
> security context will be set for #11. Do not re-open either as a
> remediation item.

- **6** `api-server/internal/middleware/middleware.go:35` — the
  `?access_token=` fallback lives in the shared `Auth` middleware, so it
  is accepted on *every* authenticated route, not just SSE. Scope it to
  `/events`.
- **7** `remote_workspace_service.go:223` — remote-workspace hostname
  validation rejects only whitespace/`/`/`@`; no private/loopback/IMDS
  denylist ⇒ authenticated in-cluster SSRF via guacd. Bounded by the
  fail-closed admin feature-gate and the protocol set (vnc/rdp/ssh only —
  **no telnet**, so no generic TCP/HTTP client / metadata exfil).
  Add an IP denylist resolved at validate- and connect-time (anti-DNS-rebind).

  > **Resolved 2026-07-25 (PR #103) — mitigated, not contained.** A
  > `HostGuard` in the api-server refuses loopback, link-local (IMDS
  > included), unspecified and multicast addresses, the kube-apiserver
  > ClusterIP (read from `KUBERNETES_SERVICE_HOST`, no configuration),
  > in-cluster name shapes (single-label, `*.svc`, `*.<cluster domain>` —
  > the domain discovered from the pod's `resolv.conf`) and any CIDR in
  > `apiServer.remoteBlockedCIDRs`. Enforced at create, update **and
  > connect**, so entries registered before the guard are covered.
  >
  > Two deliberate departures from the recommendation above. RFC1918 is
  > **not** blocked: unlike a desktop, a legitimate remote machine
  > commonly sits on a private LAN reached over VPN or peering, so what
  > is blocked is the cluster's address space, not private space. And a
  > hostname that fails to resolve is **allowed** — registering a machine
  > that is currently off must keep working.
  >
  > The recommendation's anti-DNS-rebind goal is **not** achieved and
  > cannot be at this layer: the api-server validates the name, guacd
  > resolves it when it dials. The guard closes the naive case and turns
  > a silent timeout into a readable 400 — it is not a boundary, and the
  > code and docs say so.
  >
  > **Residual, and larger than this finding:** there is **no
  > NetworkPolicy on the platform namespace at all**. The policies added
  > for findings 5/8/10 cover the desktop namespaces only, so guacd and
  > wwt still have unrestricted egress — the real amplifier here, and the
  > only thing that would actually contain it. Tracked as a hardening
  > project, not as an audit item: an egress policy on those pods must
  > still permit the in-cluster leg (guacd dials desktop pods), so it is
  > a `namespaceSelector` + `ipBlock … except <cluster CIDRs>` design,
  > not a private-range denylist.

- **8** `placement.go:162` — same ingress-only policy as #5, restated
  from the operator domain.
- **9** `workload.go:166` — override `securityContext`/`podSecurityContext`/
  `volumes` are copied verbatim with **no content validation** (only the
  *right* is policy-checked). Requires an admin to grant the right; the
  operator-created namespace's `baseline` PSA blocks the
  privileged/hostPath/host-namespace takeover primitives. Validate content
  against a hardened allow-list; enforce PSA `restricted` as a backstop.
- **10 / (15)** `workload.go:191` — desktop pods never set
  `AutomountServiceAccountToken:false`, so the (usually `default`) SA
  token is mounted into tenant-controlled, internet-facing pods. Inert
  today (no RBAC bound to that SA) but a latent escalation the day any
  binding is added. One-line fix: `AutomountServiceAccountToken: ptr(false)`.
- **11** `placement.go:71` — placed namespaces enforce PSA `baseline`
  (not `restricted`) and inject no hardened default container
  `securityContext` (root, `allowPrivilegeEscalation:true`, no seccomp,
  `CAP_NET_RAW`). Lowers blast radius of a *separate* breakout only.
- **12** `wwt/internal/kasm/kasm.go:80` — `InsecureSkipVerify: true` on
  the KasmVNC upstream hop (encrypted but unauthenticated; injected Basic
  cred rides it). Documented residual in `docs/kasmvnc.md`; needs a MITM
  position on the in-cluster hop to exploit. Pin `ServerName`+`RootCAs`
  with a cert-manager cert.
- **13** `frontend/src/stores/authStore.ts:22` — the access token is
  zustand-persisted to `localStorage` (`waas-auth`); any XSS/supply-chain
  script execution steals a non-revocable 8 h bearer. Strongly mitigated
  by the strict CSP (no `unsafe-inline`, no DOM-XSS sinks found). The real
  residual is the non-revocable token (#2). Prefer in-memory token + refresh.
- **14** `helm/waas/templates/ingress.yaml` — no `Strict-Transport-Security`
  emitted by the chart; relies on the ingress-nginx controller default,
  so the Gateway-API path and non-nginx classes get no HSTS. Emit it from
  the frontend nginx or the ingress annotations.
- **15** `helm/waas/templates/secrets-job/job.yaml:131` — bundled-Postgres
  URL pins `sslmode=disable` (the bundled PG serves no TLS, so this
  reflects reality rather than downgrading). Parameterize `sslmode` and
  wire TLS on the bundled PG; document that external-DB operators must not
  copy it.

---

## Live pentest results (dynamic)

Executed against the k3d dev env. **No exploitable break was found in the
core access-control surface** — every attack below was correctly repelled:

| Attack executed | Result |
|---|---|
| JWT payload tamper (`role→admin`, keep sig) | `401` rejected |
| `alg:none` forged admin token | `401` rejected |
| HS256 alg-confusion (JWKS pubkey as HMAC secret) | `401` rejected |
| Garbage / missing token | `401` rejected |
| Non-admin → 6 admin routes (`/users`, template writes, `/admin/*`, policies) | `403`/`404` |
| `?all=true` fleet-list as non-admin | ignored (handler hardcodes `all=false`) |
| Cross-tenant IDOR: `user` → `dev`'s workspace (Get/Delete/Connect/Pause/Resize/Overrides) | `404` (no existence leak), incl. valid bodies |
| Arbitrary PVC delete `DELETE /volumes/{ns}/{name}` | `404` (owner+managed+retained gated) |
| `ownerId` spoof on create as non-admin | ignored — owner forced to caller's `sub` |
| `PATCH /me` mass-assignment (`role`/`policy`/`groups`) | role unchanged, stays `user` |
| Override `nodeSelector`/`tolerations` (schedule onto control-plane) | `403 OverrideNotAllowed` (policy) |
| Override reserved metadata `waas.xorhub.io/owner` | `403` (reserved-key guard) |
| Login brute-force burst | `429` after the 10/min limit |
| Storage quota (incl. retained volumes) | enforced (`403 QuotaExceeded`) |
| Override `env` `valueFrom.secretKeyRef` → platform secret | **rendered into podTemplate — finding #3** |

Incidental robustness note: `PATCH …/overrides` with an identical body
returned `200`/`403`/`500` non-deterministically depending on workspace
phase and concurrent reconcile — worth a look for update-conflict
handling (not a security issue per se).

## Positive assurances (verified)

Security-relevant classes that were probed and found **well-defended**
(condensed from ~45 sourced assurances):

- **JWT**: RS256 pinned via `jwt.WithValidMethods` (no `none`/alg
  confusion), issuer + audience + expiry required; API vs connection
  tokens use distinct audiences (`waas-api` / `waas-connection`) so
  neither replays at the other surface.
- **Passwords/login**: argon2id (PHC, per-hash salt, constant-time),
  equal-cost dummy hash on unknown user, SSO-only accounts refuse
  password auth, rate-limited 10/min per client IP.
- **OIDC**: per-request state + PKCE S256 + nonce from `crypto/rand`,
  HttpOnly `SameSite=Lax` one-shot cookie, id_token + nonce verified;
  accounts keyed on immutable `sub`, username collision fails **closed**
  (no silent linking); token delivered via URL fragment; no open redirect.
- **IDOR/authz**: every by-ID route funnels through an ownership check
  returning `404`; no admin bypass on live sessions; connection tokens
  minted only for the caller's own session; kasm path binds token→`sid`
  (`403` on mismatch) — confused-deputy closed.
- **Injection**: SQL uniformly parameterized (only interpolated
  identifier is a hardcoded column constant); resize uses a fixed argv
  (no shell); K8s selectors built from server-side constants; connect-time
  params reject all platform-tier fields (hostname/port/creds) for admins too.
- **Webhook identity**: `spec.owner` + identity annotations frozen
  immutable for every caller, settable only by the api-server SA;
  `failurePolicy=Fail`; placement + PVC-adoption ownership enforced.
- **wwt**: token-validated-before-dial invariant on both paths; upstream
  host/port come from server-side `ConnectionInfo`, never the request
  (no SSRF via `sid`); browser token stripped / Basic injected
  server-side; clipboard grant is a hard bound; downloads-API path-block
  resists normalization bypass.
- **Frontend**: strict CSP (`script-src 'self'`, no `unsafe-inline`,
  `frame-ancestors 'none'`), zero DOM-XSS sinks, safe CORS (allow-list,
  dev-only), no open redirect, icon-URL resolver rejects `javascript:`/
  traversal.
- **Operator RBAC**: no wildcards, no `escalate`/`bind`/`impersonate`.
- **Supply chain**: no hardcoded prod credentials (secrets generated once
  from `/dev/urandom` / 4096-bit RSA by pre-install Jobs, `*SecretRef`
  supported); dev creds isolated to `hack/dev/`; all Dockerfiles
  distroless/non-root, digest-pinned; platform pods locked down
  (`runAsNonRoot`, drop ALL caps, `readOnlyRootFilesystem`, seccomp).

## Prioritized remediation plan

| Order | Effort | Item | Why first |
|---|---|---|---|
| 1 | ~1 h | **#1** bound/sign-check length in `ReadInstruction` + `recover()` in pipe goroutines + `SetReadLimit` | only confirmed *remotely-triggerable* impact (cross-tenant DoS) |
| 2 | ~2 h | **#3** drop `valueFrom` from override env (literal-only) | live-confirmed secret-exfil primitive |
| 3 | ~½ day | **#5/#8/#10** default-deny egress NetworkPolicy + `AutomountServiceAccountToken:false` | closes the tenant-pod pivot surface together |
| 4 | ~½ day | **#2** enforce revocation (active/role recheck or `tokensValidAfter`); ties into the planned refresh flow | privilege revocation currently ineffective ≤ 8 h |
| 5 | ~2 h | **#4/#6** dedicated short-lived SSE token; scope `?access_token=` to `/events` | stops full bearer leaking to logs |
| 6 | ~½ day | **#9/#11** validate override `securityContext`/`volumes` content; PSA `restricted` on placed namespaces | defense-in-depth against a granted-override misuse |
| 7 | ~1 h each | **#12/#14/#15** cert-pin KasmVNC upstream; emit HSTS from the chart; parameterize `sslmode`+PG TLS | close documented residuals |
| 8 | design | **#13** in-memory token + refresh (folds into #2) | removes XSS→durable-token amplification |
