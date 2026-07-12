# Prompt Fable 5 — Feature 14: be able to disable local login when OIDC is configured (`WAAS_LOGIN_OIDC_ONLY`)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context

Today, local login (username/password) and OIDC/SSO login coexist
**with no way to disable the former**, even when an IdP is properly
configured:

- **Local login** — public route `POST /api/v1/auth/login`
  (`api-server/internal/server/router.go:58`, outside the
  `middleware.Auth` group, explicit comment at the top of `New()`,
  `router.go:31-32`: _"Every /api/v1 route except login sits behind
  the JWT middleware — no bypass routes."_). Handler
  `AuthHandler.Login` (`api-server/internal/handler/auth_handler.go:37-49`)
  delegates to `AuthService.Login` (`api-server/internal/service/auth_service.go:38-80`):
  lookup by username, generic refusal if `user.PasswordHash == ""`
  (SSO-only account, line 46-51 — deliberately the same error message
  as a wrong password, anti-enumeration), bcrypt check, then JWT
  signing via `auth.NewAccessClaims` + `s.signer.Sign`
  (line 61-65).
- **OIDC login** — entirely optional: `OIDCConfig.Enabled()`
  (`api-server/internal/config/config.go:122-123`) ⇔ `IssuerURL != "" && ClientID != ""`.
  In `cmd/api-server/main.go:82-87`, `oidcSvc` stays `nil` if not
  configured; `AuthHandler` carries this nil (`oidc *service.OIDCService`,
  `auth_handler.go:22`) and already uses it as a guard for
  `OIDCStart`/`OIDCCallback` (404 `apierror.NotFound("SSO login is not
configured")`, lines 84-87 and 109-112). `OIDCService.Callback`
  (`oidc_service.go:101-139`) joins **exactly the same token-issuance
  path** as local login (`auth.NewAccessClaims` + `s.signer.Sign`,
  line 133-137) and returns the same `LoginResult` type.
- **The frontend contract ALREADY has the field needed, just never
  wired up.** `GET /api/v1/auth/providers` (`AuthHandler.Providers`,
  `auth_handler.go:63-77`) returns
  `{ local: bool, oidc: { enabled, name?, startUrl? } }` — but `Local`
  is hard-coded to `true` (line 72:
  `payload := struct{...}{Local: true}`). On the frontend side,
  `AuthProviders.local: boolean` already exists
  (`frontend/src/types.ts:325-333`) and `useAuthProviders()`
  (`frontend/src/hooks/useApi.ts:100-106`) fetches it, but
  `LoginPage.tsx` (`frontend/src/pages/LoginPage.tsx:17`) only
  destructures `providers.data?.data.oidc` — `local` is read nowhere.
  The username/password form (lines 34-74) is rendered
  **unconditionally**; the SSO button (lines 75-92) is the only thing
  conditioned, on `oidc?.enabled && oidc.startUrl`.
- **Invariant documentation that needs deliberate revisiting**:
  `OIDCService` carries an explicit design comment
  (`oidc_service.go:24-34`) — _"It exists NEXT TO local auth, never
  instead of it: local login stays available for the bootstrap admin
  and as break-glass when the IdP is down."_ — repeated in
  `config.go:68-71` (doc of `Config.OIDC`) and in
  `helm/waas/values.yaml:93-96` (comment above the `oidc:` block).
  This feature deliberately contradicts this invariant for deployments
  that explicitly ask for it (`opt-in`, never the default behavior) —
  see § Bootstrap/break-glass tension, to be addressed, not ignored.
- **Bootstrap admin, unrelated to OIDC**:
  `UserService.EnsureBootstrapAdmin` (`api-server/internal/service/user_service.go:234-266`),
  called unconditionally on every startup
  (`cmd/api-server/main.go:120`) as long as the `users` table is
  empty, always creates an admin **with a password**
  (`cfg.AdminUsername`/`cfg.AdminPassword`, random generation + log if
  absent). This mechanism never looks at OIDC and will keep creating
  this account even when local login is disabled for everyone — it's
  the core of the tension to settle (§ below).
- **The OIDC group → admin role mapping ALREADY exists, separately
  from this feature**: `OIDCConfig.AdminGroups []string`
  (`config.go:108-114`, `WAAS_OIDC_ADMIN_GROUPS`) + `GroupsClaim`
  (`config.go:109-110`, `WAAS_OIDC_GROUPS_CLAIM`, default `"groups"`).
  In `oidc_service.go`, `syncUser` (lines 176-232) assigns
  `auth.RoleAdmin` at creation **and on every subsequent login** if
  the user belongs to a group listed in `AdminGroups`
  (`adminByGroups`, lines 234-241) — but **only if `AdminGroups` is
  configured** (`oidc_service.go:219-221`:
  `if len(s.cfg.AdminGroups) > 0 { user.Role = role }`). If
  `AdminGroups` is empty, every new OIDC user gets `auth.RoleUser` at
  creation and is never touched again — "roles stay admin-managed"
  (existing comment, `config.go:114`). This feature (§ Tension below)
  must work with this existing mechanism, not ignore it.
- **No existing trace** of this feature: searching
  `OIDC_ONLY|LoginMethod|AuthMethod|local_login|disableLocalLogin|OidcOnly`
  across the whole repo → zero results. No ADR on auth
  (`docs/adr/` only contains 0001 template-boundary and 0002
  crd-evolution, neither mentions auth).
- **Boolean-flag precedent to follow** (env → Go → Helm):
  `WAAS_METRICS_ENABLED`. Struct (`config.go:66`,
  `MetricsEnabled bool`), inline parsing in `Load()`
  (`config.go:145`, `os.Getenv("WAAS_METRICS_ENABLED") == "true"` — no
  `envBool` helper, that's the file's idiom), consumed in
  `main.go:72-74` and at **two** points in `router.go` (middleware
  line 38-40, conditional route mounting line 53-55). On the Helm
  side: `values.yaml:160` (`metrics.enabled: false`), variable emitted
  **only if true** (never `value: "false"`) in
  `templates/api-server.yaml:162-167` — the same idiom already exists
  for the `oidc` block (`{{- with .Values.apiServer.oidc }}{{- if .issuerURL }}`,
  `api-server.yaml:189-223`).

## Architecture decision

1. **The new field lives in `OIDCConfig`**, not at the root level of
   `Config` — semantically it's a login policy _about_ OIDC, and it
   keeps `Enabled()` and the new validation co-located:

   ```go
   // OIDCOnly disables local username/password login entirely once
   // OIDC is configured — every account must authenticate through the
   // IdP. Requires Enabled() == true (validated at startup); the
   // bootstrap admin account still EXISTS (EnsureBootstrapAdmin is
   // unconditional) but cannot log in locally while this is set — see
   // docs/studies/18-prompt-feature14-oidc-only-login.md for the
   // break-glass procedure.
   OIDCOnly bool
   ```

   Parsing in `Load()`, next to the rest of the OIDC block
   (`config.go:151-162`): `os.Getenv("WAAS_LOGIN_OIDC_ONLY") == "true"`
   — variable name deliberately **outside** the `WAAS_OIDC_*` prefix
   (it's a global login policy, not a provider parameter), keeping the
   `WAAS_LOGIN_` prefix suggested by the original request.

2. **Fail-closed validation at startup**, next to the existing block
   `config.go:168-175`:

   ```go
   if cfg.OIDC.OIDCOnly && !cfg.OIDC.Enabled() {
       return nil, fmt.Errorf("WAAS_LOGIN_OIDC_ONLY requires OIDC to be configured (WAAS_OIDC_ISSUER/WAAS_OIDC_CLIENT_ID)")
   }
   ```

   This prevents the dangerous case "flag set to true by mistake, OIDC
   not configured ⇒ nobody can ever log in again" — the server refuses
   to start rather than silently locking everyone out.

3. **`AuthHandler` already carries `oidcCfg config.OIDCConfig`**
   (`auth_handler.go:23`, injected by `NewAuthHandler`,
   `auth_handler.go:27-29`) — **no constructor signature change
   needed**. Use `h.oidcCfg.OIDCOnly` directly:
   - `Login` (`auth_handler.go:37-49`): if `h.oidcCfg.OIDCOnly`, return
     `apierror.NotFound("local login is disabled — sign in via SSO")`
     even before decoding the body (same style as the `h.oidc == nil`
     guard of `OIDCStart`/`OIDCCallback`, lines 84-87/109-112). This
     protects the API even against a direct call that bypasses the
     frontend.
   - `Providers` (`auth_handler.go:63-77`): `payload.Local = !h.oidcCfg.OIDCOnly`
     instead of the hard-coded `Local: true` (line 72).
   - Keep the routing as-is (`router.go:57-61`) — no conditional route
     mounting like `/metrics`: the pattern already in place in this
     file for "optional feature not configured" is a guard at the top
     of the handler + 404, not a missing route
     (`OIDCStart`/`OIDCCallback` already do this for the symmetric
     case). Document this choice if you prefer the other pattern, but
     stay consistent with the existing code.

## What needs to be delivered

1. `api-server/internal/config/config.go`: `OIDCOnly bool` field on
   `OIDCConfig`, `WAAS_LOGIN_OIDC_ONLY` parsing, fail-closed validation
   described above.
2. `api-server/internal/handler/auth_handler.go`: guard in `Login`,
   dynamic `payload.Local` in `Providers` — see § Decision.
3. `frontend/src/pages/LoginPage.tsx`: when
   `providers.data?.data.local === false`, do NOT render the
   username/password form (lines 39-59) nor its submit button (lines
   68-74) — only the OIDC block (lines 75-92, remove the "or"
   separator's condition which no longer makes sense without a form
   next to it). Also handle the `useAuthProviders()` loading state:
   don't flash the form while the request is in flight then abruptly
   make it disappear — a minimal "loading" state (spinner or simply
   nothing) until `providers.isSuccess` is true, before deciding what
   to render.
4. `frontend/src/types.ts`: no change — `AuthProviders.local` already
   exists.
5. Helm — `helm/waas/values.yaml`, in the `apiServer.oidc:` block
   (lines 93-115), new key after `providerName`:

   ```yaml
   oidc:
     ...
     providerName: OIDC
     # Disables local username/password login entirely once set —
     # every account must go through the IdP above. Requires
     # issuerURL/clientID to be set (the api-server refuses to start
     # otherwise). The bootstrap admin account is still CREATED but
     # cannot log in locally while this is true — see
     # docs/studies/18-prompt-feature14-oidc-only-login.md.
     #
     # Set adminGroups (below) at the same time, or no account will
     # ever be able to reach the admin role through SSO — only the
     # bootstrap admin has it, and it becomes unreachable once this
     # flag is true.
     disableLocalLogin: false
   ```

   `helm/waas/templates/api-server.yaml`: **do NOT emit** this
   variable inside the existing `{{- if .issuerURL }}` block (lines
   190-221) — if you do, an admin who enables
   `disableLocalLogin: true` while forgetting `issuerURL` would see
   the variable silently disappear instead of triggering the startup
   error from § Decision point 2. Emit it right after the
   `{{- with .Values.apiServer.oidc }}` block (i.e. at the same level
   as `{{- if .issuerURL }}`, not inside it), only if `true` (same
   idiom as `WAAS_METRICS_ENABLED`, `api-server.yaml:162-167`):

   ```yaml
   {{- with .Values.apiServer.oidc }}
   {{- if .disableLocalLogin }}
   - name: WAAS_LOGIN_OIDC_ONLY
     value: "true"
   {{- end }}
   {{- if .issuerURL }}
   ...existing block unchanged...
   {{- end }}
   {{- end }}
   ```

6. `helm/waas/values.yaml` (or the `docs/` equivalent): if the repo has
   a generated parameters doc (`make docs-params`, mentioned in other
   studies) regenerate it so the new key shows up there.

## Bootstrap admin / break-glass tension — to be explicitly settled

The existing design comment says local login should _always_ stay
available for the bootstrap admin and as break-glass if the IdP goes
down. This feature deliberately contradicts that, but **in an opt-in
and documented way**, not silently. Do NOT code a hidden bypass (e.g.
"except for `cfg.AdminUsername`") — that would be an undocumented
backdoor in the `Providers` payload (the frontend would show "local
disabled" while the admin could still log in anyway). The chosen
approach:

- `EnsureBootstrapAdmin` stays unchanged and unconditional — the admin
  account is always created on first startup, flag or not.
- The flag cuts off local login for **everyone without exception**,
  admin included — consistency with the `Providers` payload the
  frontend sees.
- The **documented break-glass** becomes: temporarily redeploy with
  `disableLocalLogin: false` (or `WAAS_LOGIN_OIDC_ONLY` absent) to
  reopen local login, sign in with the admin account, fix the IdP,
  then set the flag back to `true`. It's a cluster-admin act
  (Helm/kubectl access), not a code workaround — in the same spirit as
  the doctrine already applied elsewhere in this repo for admin
  bypasses ("stays a visible, auditable policy, never a hidden code
  path", see
  `docs/studies/17-prompt-feature13-direct-image-deploy-waas.md` § Admin
  bypass for the precedent).
- Document this trade-off in the commit/PR **and** in the Helm comment
  of the `disableLocalLogin` field (already drafted above) — a future
  reader who enables this flag must immediately understand the
  emergency procedure before needing it urgently.

### Total lockout case: `disableLocalLogin=true` without `AdminGroups`

The break-glass above assumes there is *a* path to obtaining an admin
role afterward. That's only true if `WAAS_OIDC_ADMIN_GROUPS` is
configured (§ Context, group→role mapping). If an operator enables
`disableLocalLogin: true` **without** configuring `AdminGroups`:

- the bootstrap account created by `EnsureBootstrapAdmin` is
  unreachable (local login cut off);
- any user logging in via OIDC gets `auth.RoleUser` at creation and is
  **never** automatically promoted (empty `AdminGroups` ⇒ no role
  sync, `oidc_service.go:219-221`);
- result: **no admin account is reachable day to day**. The
  break-glass described above (reopening local login) remains
  technically possible to log in with the bootstrap account — but only
  because `EnsureBootstrapAdmin` keeps silently creating it; nothing
  in the UI/API flags this trap before an operator stumbles into it.

This isn't an exotic case: it's the likely outcome of a first go-live
where the operator enables `disableLocalLogin` to "clean things up"
without having mapped the IdP groups yet. Deliver at minimum:

- In the Helm comment of `disableLocalLogin` (§ What needs to be
  delivered, point 5): explicitly mention that `adminGroups`/
  `groupsClaim` must be configured at the same time, or nobody can
  ever get the admin role through this path.
- A startup warning log (not a fatal error — it stays startable via
  the bootstrap admin break-glass) when
  `cfg.OIDC.OIDCOnly && len(cfg.OIDC.AdminGroups) == 0`: a clear
  message such as `"WAAS_LOGIN_OIDC_ONLY is set but WAAS_OIDC_ADMIN_GROUPS
  is empty — no account can become admin via SSO; only the bootstrap
  admin (currently unreachable while this flag is set) has the admin
  role"`. Decide yourself where to place it (next to the existing
  fail-closed validation, `config.go:168-175`, or in `main.go` after
  `Load()`) and document this choice — see also § Open points.

## Constraints

- The flag must be fail-closed at startup (§ Decision point 2) — never
  let a deployment sit in a state where `OIDCOnly=true` and
  `Enabled()=false` silently coexist.
- Don't touch `OIDCService`/`OIDCStart`/`OIDCCallback` — the SSO path
  is already correct and out of scope for this fix, except for the
  design comment (`oidc_service.go:24-34`) which needs nuancing to
  reflect that the "never instead of it" now has a documented opt-in
  exception.
- Don't create a hidden bypass for the bootstrap admin (§ Tension
  above) — the flag cuts off local login for everyone or for nobody.
- The `/auth/login` route mounting (`router.go:58`) doesn't change —
  the guard lives in the handler, not the router (consistency with
  `OIDCStart`/`OIDCCallback`).
- `frontend/src/pages/LoginPage.tsx` must never show an empty/broken
  form while `useAuthProviders()` is loading, nor a form→disappear
  flash.

## Tests

- Go, `api-server/internal/config`: `Load()` with
  `WAAS_LOGIN_OIDC_ONLY=true` alone (no issuer/clientID) → error; with
  issuer+clientID+secret+redirectURL → OK,
  `cfg.OIDC.OIDCOnly == true`.
- Go, `api-server/internal/handler` (or the existing `auth_handler`
  integration test): `Login` with `oidcCfg.OIDCOnly=true` → 404,
  regardless of the username/password pair (even a valid one);
  `Providers` returns `local: false` in this case, `local: true`
  otherwise (current default case, not regressed).
- Vitest, `LoginPage.tsx`: `providers.data.local === false` ⇒ no
  `<form>`/username-password fields in the DOM, SSO button present;
  `local === true` (or absent, default value before fetch) ⇒
  unchanged current behavior; loading state doesn't show the form then
  make it disappear (no flash).
- `go build ./...` + Go tests on `api-server`; `tsc -b` + vitest on
  `frontend`; `helm template` on the chart with
  `apiServer.oidc.disableLocalLogin=true` without `issuerURL` to
  verify that the `WAAS_LOGIN_OIDC_ONLY` variable is indeed emitted
  even in that case (it's `config.go` that must refuse to start, not
  Helm that should hide it).

## Open points (your judgment call)

- Exact Helm key name (`disableLocalLogin` proposed, in the existing
  `oidc:` block) — free to rename if you find something clearer,
  document the choice. => judgment call: fine as is.
- Should there be a dedicated message on `LoginPage.tsx` explaining
  why the form disappeared (e.g. "This organization requires SSO
  sign-in") instead of simply omitting it? Not strictly necessary for
  the feature, but improves UX if a user arrives via an old
  bookmark/favorite. Add an i18n key in
  `frontend/src/i18n/locales/{en,fr}.json` (`login.*` section, => "no,
  not needed" lines 17-27 of both files) if you choose to implement
  it.
- The design comment `oidc_service.go:24-34` — free rewording as long
  as the opt-in exception is explicitly mentioned.
- Startup warning for `OIDCOnly=true && AdminGroups empty` (§
  Bootstrap admin / break-glass tension, "Total lockout case"
  subsection): startup warning log (recommended, non-fatal — the
  deployment stays usable via bootstrap admin + break-glass) vs.
  nothing more than the Helm/doc comment. If you choose to code
  nothing, justify it explicitly in the commit — don't leave this
  silence implicit as in the initial version of this study.
