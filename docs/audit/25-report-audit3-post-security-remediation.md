# Audit 3 — quality debt left by the security remediation (2026-07-25)

Quality audit of the remediation wave `v0.2.0..HEAD` (`1a72c313ae4f`),
2026-07-20 → 2026-07-25: **77 non-merge commits, 132 files, 5 965
insertions / 3 001 deletions**, 56 Go files touched of which 25 are
tests. It closes 11 of the 15 findings of
`docs/audit/security-audit-2026-07-20.md` and carries three features that
rode along (per-user namespace placement, session-cookie auth, automatic
catalog sync).

This is **not** a second security audit. Findings 9 and 11 of the July 20
report are settled by the maintainer arbitration recorded there and are
not re-opened here in any form; finding 7's residual (no NetworkPolicy on
the platform namespace) is a tracked hardening project, not a discovery.

**Granularity choice**: one finding per *actionable remedy*. Sites that
share a single fix are grouped into one row (the three copies of the
personal-namespace rule; the chart-test gaps are split, because they are
two different tests in two different files). Findings whose root cause
predates `v0.2.0` are named in one line and dropped.

**Method**: every measurement below was run, never estimated — commands
are quoted in each section. Every finding is sourced `file:line` at HEAD
plus the commit that introduced it, then re-read *trying to refute it*.
**19 candidates raised, 15 retained, 4 refuted** (§5).

**Corrections (2026-07-25)**, found while remediating F6 and recorded
here rather than silently rewritten:

1. **F6 overstated the `workspaces` sub-case.** The report claimed the
   operator stamps `labelOwner` and a ResourceQuota on the pre-existing
   shared `waas-workspaces`. It does not: `ensureNamespace` returns
   early when the namespace exists (`placement.go:41-51`), so
   `bootstrapNamespace` never runs. Corrected in §4.
2. **F6's severity drops from *high* to *medium*.** It rested partly on
   that sub-case, and partly on a rename scenario that cannot occur —
   the username is immutable after creation (written only at
   `user_service.go:80` and `oidc_service.go:225`; the OIDC re-sync
   refreshes groups, email, displayName and role, never the username).
   What remains is real: two accounts whose usernames differ only by
   what `Sanitize` erases share a namespace, its ownership label and its
   quota. Corrected in §7.

**Status: the action plan of §8 is complete.** Every order was delivered
between 2026-07-25 and 2026-07-26; §11 records what shipped for each
finding, including the three that shipped as something other than what
was prescribed here, and the three that deliberately shipped nothing.
The analysis below is kept as written — it is the point-in-time record
of what the wave looked like — so read §11 before treating any
recommendation in §7 or §8 as outstanding.

## 1. Executive summary

The wave is, on the whole, better than its speed suggests. **TODO/FIXME
count is still zero** (repo-wide grep over Go/TS/YAML/sh, excluding
`hack/`), **every new `.go` file in the range ships with a sibling
`_test.go`**, frontend coverage went from audit 2's 40.9 % to **68.31 %**,
and the two headline remediations (`ReadInstruction` bounding, override
`valueFrom` rejection) are enforced at both the early-400 and the
admission barrier. The `refactor(api-server)` performed mid-feature
(`10932b0692d4`) survives a cold re-read: both helpers it extracted have
2+ callers, exactly what `AGENTS.md` asks.

What the speed did cost is concentrated in three places.

**One fix was applied to one column instead of three.**
`c8d9268d9b55` correctly identified that a full-row `users` write carries
a copy read ~50-100 ms earlier (argon2id) and would clobber a concurrent
revocation — then removed only `tokens_valid_after` from the write.
`active` and `role` are still written back from that stale copy by
`AuthService.Login`, and those two columns are precisely what the wave's
new per-request check (`4fe23bbd5678`) now reads to enforce revocation.
An admin deactivating an account during a login silently loses the
deactivation (F5).

**The per-user placement default has a third hole, as suspected.**
Namespace names are derived from a *lossy* normalization the package
documents as lossy, with no discriminator for values that fit the budget.
`alice.smith`, `alice_smith`, `alice-smith` and `Alice Smith` all resolve
to `waas-alice-smith` (measured, §4), and the "another user's personal
namespace" guard added by `d362ba6e5fa2` is short-circuited on that path
because the collision arrives as the *resolved default*, not as a
deviation. A user named `workspaces` resolves to `waas-workspaces` — the
historical shared namespace, which they join rather than appropriate
(correction 1 above) (F6).

**The pentest's parting note was never addressed.** `RetryOnConflict` and
`IsConflict` appear **zero times** in api-server and operator production
code; the only thing the wave added is a *test* that learned to tolerate
a conflict (`3a3561573bcb`). `UpdateOverrides` still returns a bare 500
on a 409 (F7).

Complexity grew where expected but did not become unmanageable: the auth
middleware surface went from 5 functions / 18 total cyclomatic to **15 /
49** with no single function above 6 (§3). It is still explainable — the
risk is not any one function but the `fromCookie` asymmetry spread over
three of them.

The largest *unmeasured* gap is the chart: the `!` breaking placement
default has **zero** helm-unittest coverage of its env wiring, in a
design where the api-server and the operator must be fed the same value
by the same key (F11).

## 2. Refactoring needs

Measured: line deltas via `git show v0.2.0:<file> | wc -l` against HEAD.

| File | v0.2.0 | HEAD |
|---|---|---|
| `api-server/internal/middleware/middleware.go` | 112 | 302 |
| `api-server/internal/service/catalog_sync_worker.go` | 360 | 532 |
| `operator/internal/controller/placement.go` | 292 | 327 |
| `operator/pkg/naming/naming.go` | 248 | 274 |
| `frontend/src/stores/authStore.ts` | 24 | 91 |

The over-abstraction direction turned up nothing: `10932b0692d4`'s two
extractions (`catalogSyncEligible`, the `syncCatalog` trunk) have 4 and 2
callers respectively — see §5, refuted. The copy-paste direction turned
up two.

**F1 — the personal-namespace rule now has three homes.** The predicate
"is this namespace this user's own" is written three times: the
resolution itself (`naming.PersonalNamespace`), the webhook's prefix
admission, and the operator's ownership/quota decision. The prefix half
is character-for-character identical in the last two:

```go
tns == userNS || strings.HasPrefix(tns, userNS+"-")
```

`operator/internal/webhook/v1alpha1/workspace_webhook.go:318` and
`operator/internal/controller/placement.go:287`. A second caller exists,
so `AGENTS.md` says extract — and this is the predicate F6 turns out to
be wrong about, which is exactly why it should have one home.

**F2 — CIDR parsing duplicated across the module boundary, knowingly.**
`api-server/internal/service/remote_host_guard.go:63` (`ParseBlockedCIDRs`)
re-implements `operator/internal/controller/desktop_egress.go:109`
(`envCIDRList`); the comment at `remote_host_guard.go:60-62` admits it
("Not imported from there — that helper lives under `operator/internal`,
out of this module's reach"). Honest, but `shared/` exists and is already
imported by both modules.

## 3. Complexity

Measured with `go run github.com/fzipp/gocyclo/cmd/gocyclo@latest` and
`gocognit` on the touched packages, and on `git show v0.2.0:…` for the
before value.

**The auth path.** Summed over `middleware.go` (+ the new
`session_cookie.go`):

| | functions | total cyclomatic | max |
|---|---|---|---|
| v0.2.0 | 5 | 18 | 5 (`Auth`, `CORS`) |
| HEAD | 15 | 49 | 6 (`Auth`, `vetBearer`) |

**Is it still explainable? Yes — but the invariant is not local.** The
real decision tree is shallow:

```
request → accessToken()          header wins, else cookie   :81
        → fromCookie? → sameOriginRequest()  Sec-Fetch-Site :99
        → VerifyAccessToken()    signature/iss/aud/exp
        → vetBearer()            DB read: exists/active/role/iat :158
        → denySession()          expire cookie iff 401 AND cookie :143
```

Each step is 3-6 branches. What a maintainer cannot hold in one head is
that the single `fromCookie` boolean governs **three different rules in
three different functions**, each with a deliberate exception:
`middleware.go:58` (CSRF check on the cookie only) is written *directly*
rather than through `denySession`, because expiring there would hand any
cross-site page a one-request logout (`:59-62`); `denySession` expires on
401 but not on 503 (`:140-142`); and `SameOrigin` guards the routes that
*mint* the cookie but must **not** guard the OIDC callback (`:119-124`).
All three are correctly commented. None is enforced by anything but those
comments.

**The catalog worker.** 532 lines, **four** entry points into one sync
(`Run`→`syncAll`, `SyncNow`, `syncIfSourceChanged`, `OnImageEvent`→
`RunEventSync`→`syncPending`) and **three** mutexes (`mu`, `sourceMu`,
`pendingMu`). Re-read cold for the lock discipline `c59a14e3e5b5`
patched: the ordering is consistently `mu` → `sourceMu`, and no path
takes `sourceMu` before `mu` — **no deadlock, the fix is complete**
(refuted candidate, §5). What it does leave is F8: `mu` serializes across
*all* images, so a force-sync can queue behind an unrelated 10 s fetch.

Top of the repo overall is unchanged by this wave and predates it:
`Reconcile` 69 / cognit 100 (`workspace_controller.go:125`),
`specFromInput` 49 (`template_service.go:205`), `CheckLimits` 48
(`policy.go:426`). The highest-complexity function the wave itself
produced is `(*HostGuard).Check` at 11.

## 4. Logic holes

The highest-value axis, and the one the git history points at directly:
five `fix:` commits in this range land within hours of the `feat:` they
repair.

**F5 — `Login` writes back a stale `active`/`role`.**
`c8d9268d9b55`'s own commit message states the mechanism: *"A full-row
Update carries a copy read before the write; Login spends ~50-100 ms in
argon2id in between."* The fix removed `tokens_valid_after` from the
`UPDATE` (`api-server/internal/repository/user_sql.go:104-105`, rationale
at `:96-103`) and made `SetTokensValidAfter` its only writer. It did **not** remove `active` or
`role`, which that same `UPDATE` still writes:

- read: `api-server/internal/service/auth_service.go:52`
- ~50-100 ms of argon2id: `:70`
- `!user.Active` checked against the **stale copy**: `:74`
- full-row write-back: `:88`

An admin who deactivates (or demotes) an account inside that window gets
the change silently reverted, and the login succeeds. This matters *now*
in a way it did not at v0.2.0: `4fe23bbd5678` made `middleware.vetBearer`
(`middleware.go:169-176`) read exactly those two columns on every request
to enforce revocation. Every writer of `users` was enumerated:
`auth_service.go:88`, `oidc_service.go:262`, `user_service.go:172`,
`user_service.go:237` — the OIDC path has the same shape with a much
smaller window (no argon2id between read and write).

**F6 — two users, one "personal" namespace.** Measured against the real
package (isolated scratch module, `naming.PersonalNamespace`):

```
"alice.smith"  -> waas-alice-smith      "zoé"        -> waas-zoe
"alice_smith"  -> waas-alice-smith      "zoe"        -> waas-zoe
"alice-smith"  -> waas-alice-smith      "workspaces" -> waas-workspaces
"Alice Smith"  -> waas-alice-smith
"ALICE.SMITH"  -> waas-alice-smith
```

`operator/pkg/naming/naming.go:17-19` states the lossiness itself and
tells callers needing uniqueness to append `Suffix()` — but
`PersonalNamespace` (`:114-120`) only gets a suffix via `fitSegment`
(`:225-236`) when the value must be **truncated**. Short colliding
usernames get none.

The guard `d362ba6e5fa2` added does not catch it. In
`checkPlacementOwnership`
(`operator/internal/webhook/v1alpha1/workspace_webhook.go:301-334`),
branch 1 (`:306-308`, `tns == ResolvedDefaultNamespace(...)`) returns
`nil` **before** branch 3 (`:317-330`), which is where the "personal
namespace of another user" refusal lives. Under the new built-in default
the collision always arrives through branch 1. The operator then stamps
`labelOwner` (`placement.go:88-90`) and a ResourceQuota derived from that
one owner's caps (`:116-124`) on a namespace hosting two.

The `workspaces` case is narrower than it first reads (**corrected
2026-07-25**, see the header note). That username does resolve to the
historical shared namespace and `isPersonalNamespace` (`placement.go:277`)
does report it as personal — but no ownership label and no quota follow:
`ensureNamespace` returns as soon as the namespace exists
(`placement.go:41-51`, "Create-only: an existing namespace is never
mutated"), so `bootstrapNamespace` — the only writer of `labelOwner` and
of the auto-quota — is never reached. A user named `workspaces` simply
lands in the shared namespace, which is what every workspace did before
the flip. The stamping is real only on the creation path: a fresh
cluster where that user creates the first workspace, and an admin later
points a shared literal pattern at the name they took.

**F7 — update-conflict handling: still nothing.** The July 20 report's
closing note (*"`PATCH …/overrides` with an identical body returned
200/403/500 non-deterministically"*) was not addressed. Repo-wide grep
for `RetryOnConflict|IsConflict` over api-server and operator production
code returns **zero hits**. `UpdateOverrides`
(`api-server/internal/service/workspace_lifecycle.go:94` fetch → `:155`
update) has no retry; on a 409 the error misses `policyDenial` (which
only matches Forbidden-shaped denials) and falls through to
`fmt.Errorf` at `:160` → 500. That is the reported symptom exactly.
`SetPaused` (`:36`) and `Reload` (`:241`) share the shape. What the wave
added instead is `repointSource` in
`api-server/internal/service/catalog_sync_worker_test.go:120-140` — a
test taught to retry on conflict because "the worker status-patches the
same object as soon as a sync lands". The test papers over the class.

**F9 — a password change silently kills the current session.**
`UserService.UpdateProfile` revokes every token on a password change
(`user_service.go:240-249`), including the browser's own. No new cookie
is minted — `SetSessionCookie` is called only at
`handler/auth_handler.go:58` (login) and `:205` (OIDC callback) — and the
SPA treats the response as an ordinary profile save
(`frontend/src/hooks/useApi.ts:461-466`,
`frontend/src/pages/ProfilePage.tsx:243-251`). The user appears signed in
until the next request 401s, then lands on the login page with the
generic "session ended" notice. Re-minting on the spot would not work
either: a token issued in the same second as the bound is itself rejected
(`middleware.go:189-205`, documented and deliberate) — so the honest fix
is an explicit "password changed, sign in again", not a silent re-mint.

**F10 — the availability trade is documented but not bounded.**
`docs/governance.md:273-281` (`ac7b8bafb7d5`) states the trade honestly:
one primary-key read per authenticated request, 503 not 401 on failure,
no cache on purpose. What is missing is any bound on the read:

- no cache (deliberate, correct);
- **no Postgres pool cap** — `SetMaxOpenConns` appears once in the whole
  module, on the sqlite branch (`api-server/internal/database/database.go:61`);
- **no per-request timeout** — `router.go:38-55` uses no
  `chimiddleware.Timeout`, and `main.go:204` keeps `WriteTimeout: 0` for
  SSE, so a slow read is bounded only by the client.

A Postgres slowdown therefore converts into unbounded connection growth
against a server whose default `max_connections` is 100, i.e. a slowdown
amplifies into the full-dark outage the doc describes as the worst case.
This is not the N+1 class the July 8 audit flagged (one read per request,
not per row) — that part is refuted, §5.

**F12 — `allowInternet` is IPv4-only, silently.**
`desktopEgressRules` emits a single `0.0.0.0/0` peer
(`operator/internal/controller/desktop_egress.go:174-180`), and
`validateExceptCIDRs` refuses IPv6 entries in `blockedCIDRs`
(`:141-143`). On a dual-stack cluster a placed namespace therefore gets
default-deny egress with **no IPv6 allowance at all** — every IPv6
destination is denied even with `allowInternet: true`. DNS still works
(the port-53 rule has no `To`, `:168-173`). `extraAllowedCIDRs` accepts
IPv6 and is the workaround
(`operator/internal/controller/desktop_egress_test.go:115` proves it),
but nothing says so: `values.yaml:299-331` and `docs/placement.md:145-162`
never mention address families.

**Seams checked and clean.** `HostGuard` without `KUBERNETES_SERVICE_HOST`
(`remote_host_guard.go:87-91`, nil → guard stays active for everything
else) and `DiscoverClusterDomain` without a readable `/etc/resolv.conf`
(`config/cluster_domain.go:26-28` → `cluster.local`) both degrade as
documented. The `waas-stream` audience is sealed in both directions
(`middleware.go:208-239`: a stream token in an `Authorization` header and
an API bearer in the query string are each rejected).

## 5. Test coverage

All figures measured on 2026-07-25 at `1a72c313ae4f`. Commands:
`go test -count=1 -coverpkg=./... ./...` per module (api-server), plus
`hack/ci/coverage-ratchet.sh` for the per-package split; `go test -cover
./...` elsewhere; `npm test -- --coverage --coverage.all
--coverage.include='src/**/*.{ts,tsx}'` excluding tests/`types*.ts`/`.d.ts`.
No test or gate was weakened to obtain any of them.

| Zone | 11/07 (audit 2) | 25/07 | Reading |
|---|---|---|---|
| api-server `internal/handler` | 55.3 % | **55.9 %** | floor 40, holds |
| api-server `internal/repository` | 77.7 % | **78.1 %** | floor 50, holds |
| api-server `internal/service` | 65.3 % | **72.7 %** | up |
| api-server `internal/middleware` | 76.6 % | **86.4 %** | the wave's own surface, well covered |
| api-server `internal/server` | — | **96.1 %** | |
| api-server total | — | **70.1 %** (2 620/3 740 stmts) | |
| operator `internal/controller` | 72.7 % | **75.9 %** | up |
| operator `internal/webhook` | 86.5 % | **87.2 %** | |
| operator `pkg/policy` | 78.7 % | **95.8 %** | audit 2's C20 regression is closed |
| operator `pkg/naming` | 97.3 % | **96.2 %** | |
| wwt `guac` / `proxy` | 87.4 / 65.0 % | **89.0 / 64.9 %** | |
| shared `auth` | 69.4 % | **92.9 %** | audit 2's C19 is closed |
| **frontend (all of `src/`)** | 40.9 % | **68.31 %** (1 966/2 878 stmts) | 56 files, 369 tests |

**New files: full parity.** Every non-test file added in the range has a
sibling test — `cluster_domain.go`, `session_cookie.go`,
`remote_host_guard.go`, `desktop_egress.go`. No new file ships untested.

**New branches nothing enters.** The gaps are narrow and cheap:

- `catalog_sync_worker.go:302` `syncPending` — **42.9 %**: the Get-failure
  and NotFound legs are never entered. One fake-client table row.
- `frontend/src/pages/AuthCallbackPage.tsx` — **0 %** (lines 15-61),
  rewritten by this wave (`6a554248046a`) and one of only two places a
  session is established. Its `clearLocal('no-session')`-not-`logout`
  decision (`:41-52`) is exactly the confusion `5e4b9fcd5286` had to fix
  elsewhere, and nothing guards it.
- `frontend/src/App.tsx` — **0 %** (lines 22-114), also rewritten
  (`+44`). `useSessionBoot` (`:49-79`) encodes the 401-vs-anything-else
  distinction that F10's whole 503 design depends on. Two `it()` blocks.

**Tests that assert the fix rather than the behaviour.** One clear case:
`api-server/internal/service/catalog_sync_worker_test.go:120-140`
(`3a3561573bcb`) — a retry loop added so a test can tolerate the very
conflict production code does not handle (F7). By contrast the
`tokens_valid_after` regression test
(`api-server/internal/repository/suites_test.go`, `c8d9268d9b55`) reads as
a contract, not a symptom, and `frontend/src/stores/authStore.test.ts:20-24`
(no key or value matching `/token/i`) is an invariant guard in the style
the repo already uses for registries — both fine.

**The two new chart defaults, both directions.**

| Value | default tested | opt-out tested |
|---|---|---|
| `operator.desktopEgress.*` | yes (`tests/operator_test.yaml:46`) | yes (`:69`) |
| `apiServer.streamTokenTTL` | yes (`api_server_stream_token_test.yaml:7`) | yes (`:13`) |
| `apiServer.clusterDomain` / `remoteBlockedCIDRs` | yes (`api_server_remote_guard_test.yaml:7`) | yes (`:16`) |
| `postgres.sslMode` | yes (`secrets_job_test.yaml:55`) | yes (`:65`, `:76`) |
| **`defaultPolicy` minus `volumes`** (`!`) | negative only (`default_policy_test.yaml:84-96`) | **no** — F13 |
| **per-user placement** (`!`) | **no** | **no** — F11 |

**`codecov.yml`, stated plainly.** With `project: {target: auto,
threshold: 1%}`, `patch: off` and `carryforward: true`, api-server's 3 740
statements at 70.1 % give a 1 % band of ~51 entirely-uncovered new
statements before the project status turns red. So: a 5 800-line wave
landing with *no* new tests at all would have breached the project
threshold — the gate is not toothless at wave scale. But the wave did not
land as a wave; it landed as 77 commits across many PRs, and **each one
individually fits inside that band with room to spare**, while `patch:
off` means the diff's own new lines are never required to be covered by
anything. `patch: off` was the right call when the frontend sat at 7.9 %
and every PR would have been red; at 68 % frontend / 70 % api-server that
justification is gone. `patch: {informational: true}` would surface the
number without blocking.

## 6. New technical debt

**The discipline that held.** TODO/FIXME across Go, TS, YAML and shell:
**zero**, as in July 8 and July 11. No dead `localStorage` credential
path survived the cookie switch — the only remaining write is the
deliberate one-time eviction of the old persist key
(`frontend/src/stores/authStore.ts:22-26`), self-documented as removable
"once no deployed browser predates that release". No new Helm value is
undocumented: all five carry helm-docs `# --` comments and appear in the
generated `helm/waas/README.md`; none is a dead knob.

**The debt the wave created.**

- **F11/F13** (chart tests, §5) — the two `!` changes are the two least
  tested.
- **F14 — the `volumes` removal has no upgrade note anywhere a user
  looks.** `fc614eb2a368`'s `BREAKING CHANGE:` footer carries the
  remediation ("grant the right explicitly via
  `defaultPolicy.overrides.allowedFields`") and release-please will
  render it into `helm/waas/CHANGELOG.md` at the next chart release — so
  the changelog is not the gap. The gap is `docs/governance.md:331`,
  which still lists `volumes` as a governable field with no note that the
  bootstrap default stopped granting it, and
  `docs/accepted-limitations.md:70`, which describes the post-change
  world as if it had always been so. The placement flip, by contrast, is
  properly documented (`docs/placement.md:43-57`, including that retained
  PVCs do not follow) — nothing inside `helm/waas/` points there, though.
- **F15 — the security report's own status table is now stale.**
  `docs/audit/security-audit-2026-07-20.md:29` still records finding 13
  (token in `localStorage`) as *"Deferred — closed by the planned
  token-refresh work, not on its own"*. `6a554248046a` closed it outright:
  the SPA stores no credential at all. A status table that under-reports
  what shipped is the one kind of drift this repo's audit trail cannot
  afford. (Statement of record only — finding 13 is not re-audited here.)
- **Small, cheap, real**: `frontend/src/types.ts:125-126` —
  `LoginResult.expiresAt` has zero references anywhere in `src/`, and
  `accessToken` is deliberately discarded (`useApi.ts:88-95`);
  `frontend/src/hooks/useApi.ts:460` still says "refreshes the
  *persisted* auth user" when nothing is persisted;
  `helm/waas/values.yaml:648-654` says `postgres.sslMode` is ignored when
  `postgres.enabled` is false but not that it is equally ignored when
  `secretsJob.enabled` is false (the URL is composed only in
  `templates/secrets-job/job.yaml:133`); the three new list values are
  rendered with a bare `join ","` (`operator/deployment.yaml:68,70`,
  `api-server/deployment.yaml:75`) so an explicit `null` fails the render
  rather than degrading.

**Deferred security findings — cost of carry, not re-audited.**
**#12** (`wwt/internal/kasm/kasm.go:80`, `InsecureSkipVerify` on the
KasmVNC upstream hop): unchanged by this wave; it is the stated
prerequisite for taking KasmVNC out of experimental, so the carry cost is
a feature that cannot graduate, not a growing risk. **#13**: superseded —
see F15.

**CI / dev-env** (sized deliberately small: in scope, not where the risk
is). The range's CI churn is dependency bumps plus workflow-permission
plumbing for the Codecov OIDC upload; nothing regressed. Two observations
worth one line each: `097e9cef710c` (*pin dev-ssh to the shared namespace
its seeded Secret lives in*) is the placement flip's dev-env fallout,
landing the same day — evidence the flip's blast radius was discovered
rather than designed; and the codecov posture (§5) is the only CI gate
this audit asks to revisit.

## 7. Findings table

| # | Finding | Axis | Component | Source (`file:line`) | Introduced by | Severity | Complexity | Worth it? |
|---|---|---|---|---|---|---|---|---|
| F1 | Personal-namespace predicate written three times; the prefix rule is byte-identical in two | refactor | operator | `webhook/v1alpha1/workspace_webhook.go:318`, `internal/controller/placement.go:287`, `pkg/naming/naming.go:114` | `aa76c9ac25e4`, `d362ba6e5fa2` | medium — three copies of the rule F6 shows to be wrong; a fix must land in all three | S | **Yes**: fold into the F6 fix — one `naming.IsPersonalNamespaceOf(user, ns)` with the collision guard inside, and the bug can only be fixed once |
| F2 | `ParseBlockedCIDRs` duplicates `envCIDRList` across the module boundary, knowingly | refactor | api-server / operator | `service/remote_host_guard.go:63` vs `controller/desktop_egress.go:109` | `82d60bd68b9b`, `70b8304bef1b` | low — both tested, divergence would be cosmetic | S | **No**: `shared/` would gain a package for ~15 lines; the comment already records the choice. Revisit at a third caller, per the repo's own D4 precedent |
| F3 | Auth surface 5→15 functions, 18→49 total cyclomatic; one `fromCookie` boolean drives three rules with three exceptions in three functions | complexity | api-server | `middleware/middleware.go:58,143,125` | `7d053851a805`, `616cb5ba5290` | medium — every rule is correct and commented, none is enforced by a test asserting the *asymmetry* | S | **Yes**, as one table-driven test: cookie+cross-site ⇒ 401 and cookie NOT cleared; cookie+401 ⇒ cleared; cookie+503 ⇒ NOT cleared. Ten minutes, pins the three exceptions that reading alone protects today |
| F4 | Catalog worker: 532 lines, 4 sync entry points, 3 mutexes | complexity | api-server | `service/catalog_sync_worker.go:63-97` | `b7ac606398c8`, `c59a14e3e5b5` | low — lock order verified consistent, no deadlock; cost is comprehension | M | **Debatable**: no rewrite. Add the lock-order rule to the struct comment (`mu` before `sourceMu`, never the reverse) so the next contributor inherits the invariant instead of re-deriving it |
| F5 | `Login` writes back a stale `active`/`role`, undoing a concurrent deactivation or demotion — the same race `c8d9268d9b55` fixed for one column only | logic hole | api-server | `service/auth_service.go:52,74,88`; `repository/user_sql.go:104-105`; also `oidc_service.go:262` | `898b99eae89b` / `c8d9268d9b55` (incomplete) | **high** — the wave made these two columns the substrate of revocation (`middleware.go:169-176`), then left them clobberable in a ~50-100 ms window | S | **Yes**: the reasoning is already written in the repository doc comment; applying it to `active`/`role` means narrowing `Login`'s write to `last_login_at`/`updated_at`. Under a day, closes a hole the fix's own rationale predicts |
| F6 | Two distinct usernames resolve to one "personal" namespace (lossy sanitization, no discriminator); the another-user guard is short-circuited because the collision arrives as the resolved default | logic hole | operator | `pkg/naming/naming.go:114-120,225-236`; `webhook/v1alpha1/workspace_webhook.go:306-308` (branch 1 precedes `:317-330`); `controller/placement.go:88,116` | `aa76c9ac25e4`, `60532d2de871` | **medium** (corrected from *high*, 2026-07-25) — the first creator's ownership label and ResourceQuota govern a namespace hosting two owners, so the second's workspaces spend the first's budget. The `workspaces` sub-case carries no such stamping (create-only, §4) | M | **Yes**: this is the third hole in the feature, and it defeats the isolation the `!` breaking default was taken for. Either suffix every personal namespace unconditionally, or refuse creation when a distinct username already owns the resolved name |
| F7 | No update-conflict handling in production code; the wave added a *test* that retries instead. `PATCH /overrides` still 500s on a 409 | logic hole | api-server | `service/workspace_lifecycle.go:94,155,160`; zero hits for `RetryOnConflict\|IsConflict` in api-server + operator | pre-existing, surfaced by the 07-20 pentest; `3a3561573bcb` papers over it | **high** — a user-visible non-deterministic 500 on a routine action, reported and still open | M | **Yes**: `retry.RetryOnConflict` around fetch→mutate→Update in `UpdateOverrides`/`SetPaused`/`Reload`, and map a surviving conflict to 409 not 500. Directly closes the pentest's only open observation |
| F8 | The global sync mutex serializes across images: an admin force-sync waits behind an unrelated image's 10 s fetch, inside an HTTP request with no handler timeout | logic hole | api-server | `service/catalog_sync_worker.go:71,181-183`, `catalogFetchTimeout` `:38`; `server/router.go:38-55` (no `Timeout`) | `726c0ec55276`, `10932b0692d4` | low-medium — bounded to ~10 s by Go's mutex starvation mode, but it is a user-facing PUT | S | **Debatable**: per-image locking is the clean answer and is more machinery than the symptom deserves. Cheaper and worth doing: bound the force-sync path with its own context deadline so the API answers 503 rather than hanging |
| F9 | A self-service password change revokes the current session with no re-mint and no UI signal; the user discovers it as a mystery expiry | logic hole | api-server / frontend | `service/user_service.go:240-249`; `handler/auth_handler.go:58,205` (only mint sites); `frontend/src/hooks/useApi.ts:461-466` | `25ffb30a6373` + `6a554248046a` | medium — correct security behaviour, presented as a bug | S | **Yes**: silent-logout-after-your-own-action is the kind of thing that gets reported as data loss. A re-mint cannot work (same-second rule, `middleware.go:189-205`), so the fix is honest UX — sign out explicitly with "password changed, sign in again" |
| F10 | The per-request DB read has no cache (deliberate), **and** no pool cap and no request timeout: a Postgres slowdown amplifies into unbounded connections | logic hole | api-server | `middleware/middleware.go:159`; `database/database.go:61` (sqlite only); `server/router.go:38-55`; trade documented `docs/governance.md:195-203` | `4fe23bbd5678`, `ac7b8bafb7d5` | medium-high — the documented worst case ("fully dark") is reachable from a mere slowdown, not just an outage | S | **Yes**: `SetMaxOpenConns`/`SetConnMaxLifetime` on the pgx path is a few lines and turns an amplifier into a queue. The no-cache decision stays untouched — it is correct and is what closes the revocation window |
| F11 | Zero helm-unittest coverage of `WAAS_DEFAULT_NAMESPACE_PATTERN` in either Deployment — the `!` breaking default, in a design where both components must be fed the same key | test | helm | `templates/operator/deployment.yaml:73-74`, `templates/api-server/deployment.yaml:50-51`; grep of `helm/waas/tests/` returns 0 | `60532d2de871` | medium — the webhook comment (`workspace_webhook.go:58-60`) says the two values MUST match; nothing proves the chart wires both | S | **Yes, quick win**: two `asserts` in the existing `operator_test.yaml`/an api-server suite. The Go side of the flip is well tested; only the chart→env seam is blind, and that seam is where "MUST match" is enforced by nothing |
| F12 | `allowInternet` grants IPv4 only; a dual-stack cluster gets default-deny egress with no IPv6 allowance, undocumented | logic hole / debt | operator / helm | `controller/desktop_egress.go:174-180,141-143`; `values.yaml:299-331`; `docs/placement.md:145-162` | `70b8304bef1b`, `463dea4c7ae9` | medium — silent loss of connectivity on IPv6-enabled clusters, diagnosed only by reading the rendered policy | S | **Yes** for the documentation (a sentence in `values.yaml` naming `extraAllowedCIDRs` as the IPv6 route — the test at `desktop_egress_test.go:115` already proves it works); **No** for auto-emitting `::/0`, which would loosen the posture on clusters that never asked for it |
| F13 | `default_policy_test.yaml` asserts only that `volumes` is absent; the positive default list and the re-grant direction are untested | test | helm | `helm/waas/tests/default_policy_test.yaml:84-96` (negative), `:98-114` (generic override, not `volumes`) | `fc614eb2a368` | low-medium — a regression silently dropping `env` or `schedule` from `values.yaml:146` passes the whole suite | S | **Yes, quick win**: one `equal` on `[env, resources, schedule]` plus one re-grant case. The re-grant path is the documented remediation for the breaking change and has never been rendered once |
| F14 | The `volumes` removal has no upgrade note in the docs a user reads: `governance.md` still lists it as governable, `accepted-limitations.md` describes the end state as if it were always so | debt / docs | docs | `docs/governance.md:331`, `docs/accepted-limitations.md:70` | `fc614eb2a368` | medium — the operator meets the change as an admission failure; the remediation exists only in the commit footer (and, at release, the chart CHANGELOG) | S | **Yes**: two sentences. `AGENTS.md` requires docs to move in the same change that makes them stale, and this one did not — cheapest doctrine debt in the report to clear |
| F15 | The July 20 report's status table still calls finding 13 "Deferred" although the cookie switch closed it outright | debt / docs | docs | `docs/audit/security-audit-2026-07-20.md:29` vs `frontend/src/stores/authStore.ts:22-26` | `6a554248046a` | low — no code impact, but the audit trail under-reports what shipped | S | **Yes**: one table cell. The report is explicitly kept as a point-in-time record *with a live status column*; a stale status column is the one part that misleads |

## 8. Action plan

Order reuses the Complexity and Worth-it values set row by row above; no
second estimate is introduced here.

| Order | Findings | Effort | Why this order |
|---|---|---|---|
| 1 | **F5** (narrow `Login`'s write) | S | The wave's own rationale, applied to the two columns it skipped. Highest severity per hour in the report |
| 2 | **F7** (`RetryOnConflict` + 409 mapping) | M | The only open item the live pentest left; a user-visible 500 today |
| 3 | **F11** + **F13** + **F14** + **F15** | S (one sitting) | The chart/doc gaps around the two `!` breaking changes. Four small edits that make the breaking changes discoverable and regression-proof |
| 4 | **F10** (pool cap) + **F8** (force-sync deadline) | S | Two small bounds that stop a slowdown from becoming an outage |
| 5 | **F6** + **F1** (one fix, one home) | M | The feature's third hole. Sized above F5/F7 because the remedy is a product decision — suffix always, or refuse on collision — not just code |
| 6 | **F9** (explicit sign-out after password change) | S | UX correctness; pairs naturally with anything touching `ProfilePage` |
| 7 | **F3** (asymmetry test) + missing branch tests: `syncPending` error legs, `AuthCallbackPage`, `App.useSessionBoot` 401-vs-503 | S | The "simple test" tier: table rows, not harnesses, all on live auth paths |
| 8 | Codecov: `patch: {informational: true}` (§5) | S | The 7.9 %-frontend justification for `patch: off` no longer holds at 68 % |
| — | **F2**, **F4**, **F12** (code half) | — | Verdicts **No**/**Debatable**: attach to the next change that touches them, no dedicated effort |

## 9. Refuted

Four candidates were raised and killed by re-reading the cited code.

- **"The mid-feature refactor `10932b0692d4` is premature abstraction."**
  False. `catalogSyncEligible` (`catalog_sync_worker.go:102`) has four
  callers (`:151`, `:196`, `:271`, `governance_service.go:475,538`) and
  the `syncCatalog` trunk (`governance_service.go:536`) has two
  (`AdminUpsertImage`, `AdminSyncImage`), with sentinel errors each caller
  maps differently. `AGENTS.md`'s rule — extract at the second caller — is
  satisfied, not violated, and the sentinels are what let the same trunk
  serve a silent skip and a 400/503.
- **"`c59a14e3e5b5` patched one line and left the rest of the worker's
  lock discipline broken."** False. Every path was traced: `mu` is taken
  before `sourceMu` in `syncLocked`→`recordSource` and in
  `syncIfSourceChanged`→`sourceChanged`; `OnImageEvent` takes `sourceMu`
  and `pendingMu` but never `mu` (it must not block the shared watch
  goroutine); nothing takes `sourceMu` then `mu`. The ordering is
  consistent and the fix is complete. Only the comprehension cost remains
  (F4).
- **"The per-request user check re-creates the N+1 the July 8 audit
  flagged."** False. That N+1 was a per-workspace template `Get` inside a
  15 s quota poll (closed in audit 2 via `policy.OwnerLoads`). This is one
  primary-key read per *request*, constant in the size of the response.
  The availability concern is real but different, and is filed as F10.
- **"`HostGuard` misbehaves outside a cluster (no `KUBERNETES_SERVICE_HOST`,
  no `resolv.conf`)."** False. `remote_host_guard.go:87-91` leaves
  `apiServerIP` nil and keeps every other rule active;
  `config/cluster_domain.go:26-28` falls back to `cluster.local`. Both
  degradations are deliberate, commented, and covered
  (`cluster_domain_test.go`, `remote_host_guard_test.go`).

Also examined and **not** filed, root cause predating `v0.2.0`:
`readConfigMapKey`/`readSecretKey` at 0 % coverage
(`catalog_sync_worker.go:503,521` — both present at `v0.2.0`);
`AdminSyncImage` handler at 0 % (`governance_handler.go:83`, present at
`v0.2.0`); `middleware.CORS` at 0 % (dev-only, pre-existing); the
duplicated `envOr` (audit 2, C10, verdict **No** and still right).

## 10. Security spillover

One item, and it is the security face of a quality finding already filed
above — not a new audit line.

- **F5** is exploitable as a race, not merely as a correctness defect: an
  attacker who can trigger a login (their own credentials suffice) during
  the ~50-100 ms window in which an admin deactivates or demotes that
  account causes the change to be silently reverted in the database. The
  per-request check (`middleware.go:169-176`) then reads the resurrected
  row and admits the session. The window is small and not directly
  attacker-triggerable at will, which is why it is filed as a logic hole
  at high severity rather than escalated; the remedy is the same
  one-column-scope narrowing.

Nothing else in this audit turned up a new exploitable weakness.

## 11. Delivered (2026-07-26)

Added after the fact. §7 and §8 above are the analysis as it stood on
2026-07-25 and are deliberately not rewritten; this is what actually
shipped against them. Three findings landed as something *other* than
what was prescribed, and saying so is the point of this section — a
status that under-reports, or over-reports, is the drift F15 was about.

| # | Shipped | Where |
|---|---|---|
| F5 | `Login`'s write narrowed to `last_login_at`/`updated_at`; `role`/`active` left to their targeted setters | PR #109 |
| F7 | `RetryOnConflict` around fetch→mutate→Update in the lifecycle paths, surviving conflicts mapped to 409 | PR #110 |
| F11 | helm-unittest asserts on `WAAS_DEFAULT_NAMESPACE_PATTERN` in both Deployments | PR #111 |
| F13 | positive default list and the re-grant direction asserted | PR #111 |
| F14 | `governance.md` and `accepted-limitations.md` updated for the `volumes` removal | PR #111 |
| F15 | the July 20 report's status cell for finding 13 corrected | PR #111 |
| F10 | `SetMaxOpenConns`/`SetConnMaxLifetime` on the pgx path | PR #112 |
| F8 | force-sync bounded by its own deadline, answering 503 rather than hanging | PR #112 |
| F1 | `naming.IsPersonalNamespaceOf` — one home for the rule, webhook and operator wired to it | PR #113 |
| **F6** | **not as prescribed.** Neither "suffix every personal namespace" nor "refuse creation when a distinct username already owns the resolved name": the collision is refused at the **identity door** instead — `409` at account creation, a failed SSO provisioning audited `user.sso_placement_conflict`. A directory already numbers its homonyms (`jdoe`, `jdoe2`) and a generated discriminator would put a hash in *every* namespace name to serve a case a well-formed directory does not produce. Rationale in `api-server/internal/service/username_placement.go` and `docs/accepted-limitations.md` §4. Usernames that leave no DNS-1123 character at all (Cyrillic, CJK, Greek, Arabic) are the one case that *does* get a generated segment — the first and last groups of the account id — because nothing readable survives for them anyway | PR #113 |
| F9 | server expires the session cookie in the `PATCH /me` response; the SPA signs out naming the reason. Extended beyond the finding to the admin path (an admin editing their **own** account), through one `middleware.EndSession` + an `X-Waas-Session-Ended` header the api layer is the only reader of — plus a floor the finding did not ask for: the last active administrator cannot lose their rights | PR #114 |
| **F3** | **nothing — it was already covered.** `middleware/session_cookie_test.go` holds the exact three-exception table §7 asks for (cookie+401 ⇒ cleared, cookie+503 ⇒ not cleared, cookie+cross-site ⇒ not cleared). This audit missed it | — |
| branch tests (§8, order 7) | `syncPending`'s error legs 42.9 → 85.7 %, `AuthCallbackPage` 0 → 95 %, `App.useSessionBoot` extracted to its own module and covered 91.7 % | PR #115 |
| codecov (§8, order 8) | `patch: off` → `patch.default.informational: true`; `docs/ci-github.md` gains what actually blocks a merge | PR #116 |
| F2 | nothing, as recommended — revisit at a third caller | — |
| F4 | nothing. The lock-order rule was **not** added to the worker's struct comment; the "Debatable" verdict stands, so it waits for the next change touching that file | — |
| F12 | nothing. Both halves are still open: the `values.yaml` sentence naming `extraAllowedCIDRs` as the IPv6 route was recommended and never written, and auto-emitting `::/0` stays refused | — |

Two prescriptions in §7 are superseded by the above and are left in
place only as the record of what was thought at the time: F6's
"Worth it?" still proposes the two remedies that were dropped, and F1's
describes `IsPersonalNamespaceOf` as carrying the collision guard inside
it — the predicate exists, but the guard lives at the identity door.
