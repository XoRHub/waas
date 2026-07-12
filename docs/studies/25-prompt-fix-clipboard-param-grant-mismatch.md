# Fable 5 Prompt — Fix: `disable-copy`/`disable-paste` (connection settings) influence neither the wwt clipboard filter nor the session menu

Paste this document as-is as an implementation prompt. It assumes
that you (Fable 5) have no prior conversation context.

## Repo context: two clipboard sources of truth that don't talk to each other

The protocol parameter registry (`operator/pkg/params/params.go:122-129`)
declares two booleans for `vnc`/`rdp`/`ssh`:

```go
Name: "disable-copy", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
Description: "Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable.",

Name: "disable-paste", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
Description: "Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable.",
```

This is a field of the template/workspace "connection settings" (like
any registry param: lockable on the template side, editable in
`userParams`/`protocolParams` depending on rights — cf.
[[waas-protocol-forms-feature3]] for the ParamField UI and
[[waas-userparams-cat-syntax-feature10]] for `cat:` resolution).

**Finding verified in the code (not a guess)**: `disable-copy`/
`disable-paste` are read NOWHERE outside the registry itself
(definition + `ValidateUserOverrides`/`ValidateTemplateParams`
validation + form). `grep -rn "disable-copy\|disable-paste"` outside
`params.go`/tests/docs only turns up their generic transit through the
params map — never a lookup by name.

Meanwhile, two very real mechanisms, DISCONNECTED from each other,
govern the clipboard:

1. **guacd itself**, via `ConnectionInfo.Params` →
   `guac.ConnectionParams.Extra` → `handshake.go:paramValue` (Extra
   wins over the built-in defaults). The values from `entry.Params`
   (template) merged with `session.Params` (connection overrides,
   `workspace_service.go:644-659`) therefore do go to guacd, which for
   RDP natively applies `disable-copy`/`disable-paste` as a FreeRDP
   clipboard-channel restriction. **This path looks correct** — to be
   re-verified in practice during your verification phase (Tests
   section below), not just by reading the code.
2. **wwt's clipboard filter** (`wwt/internal/guac/clipboard.go`,
   `ClipboardFilter`) and **the capability shown in the session menu**
   (`SessionOverlay.tsx`, "copy from workspace" / "paste to workspace"
   toggles, lines 138-139 and 273-303) share the SAME source:
   `clipboardGrant`/`resolveClipboardGrant`
   (`api-server/internal/service/workspace_service.go:526-585`), which
   ONLY looks at `policy.ClipboardOf(pol)` — i.e.
   `WorkspacePolicy.Spec.Clipboard.{CopyFromWorkspace,PasteToWorkspace}`
   (`operator/pkg/policy/policy.go:164-174`). The resulting grant is
   signed into the token (`auth.NewConnectionClaims`,
   `workspace_service.go:478` and `remote_workspace_service.go:438`)
   and is used both to build the `ClipboardFilter` on the wwt side
   (`wwt/internal/proxy/proxy.go:142`,
   `guac.NewClipboardFilter(claims.Clipboard.Copy, claims.Clipboard.Paste)`)
   AND to populate `ConnectResult.Capabilities` (`workspace_service.go:488-491`),
   which the frontend displays as-is.

**Concrete result (the reported bug)**: a workspace with
`disable-copy: true` in its connection settings still shows
"copy from workspace: allowed" in the session menu as soon as the
user's `WorkspacePolicy` allows copy — and the wwt filter genuinely
lets the `guacd→browser` clipboard flow through, since it never
consults this parameter. The code comment at
`workspace_service.go:468-476` ("Enforced by the wwt proxy") documents
an intent that was never wired up: `resolveClipboardGrant` is both
protocol-agnostic AND param-agnostic — so far only the kasmvnc angle
of this hole had been noted ([[waas-kasmvnc-governance-feature11]]),
but it equally affects vnc/rdp/ssh via these two dedicated params.

## What needs to be delivered

1. **Compute the effective grant = policy AND params, never more
   permissive than either one.** `disable-copy: true` must force
   `copyFrom = false` regardless of policy; `disable-copy` absent or
   `false` leaves the policy to decide alone (this is NOT a "force
   allow"). Same symmetric rule for `disable-paste`/`pasteTo`.
2. **Wire this into both `resolveClipboardGrant` call sites**
   (`workspace_service.go:477` in `Connect`, and
   `remote_workspace_service.go:437` in its remote equivalent) by
   passing them the session's EFFECTIVE params map, i.e. the same
   merge that `ConnectionInfo` already does: template/registered-entry
   params first, then `session.Params`/`in.Params` on top
   (`workspace_service.go:650-656` for local; to be replicated for
   remote where `entry.Params` — the stored one — and `in.Params` play
   the same role, cf. `remote_workspace_service.go:418-430`).
   - Concrete trap in `Connect()` (`workspace_service.go:395-452`):
     the template is fetched ONLY if `len(in.Params) > 0 ||
in.Protocol != ""`. A workspace whose `disable-copy: true` lives
     only in the template (no override at connection time) triggers
     neither — so `entry.Params` must be resolved independently of
     this condition before calling `clipboardGrant` (line 477),
     without duplicating the existing Kube call if the template was
     already fetched earlier in the same call.
3. **A `map[string]string` → boolean-override conversion point**,
   probably a small function in `workspace_service.go` (or the
   `params` package if you'd rather co-locate it with the registry)
   along the lines of `clipboardParamOverride(params map[string]string) (blockCopy,
blockPaste bool)` reading `params["disable-copy"]`/`["disable-paste"]`
   via `strconv.ParseBool`. **Fail-closed on parse error** (a malformed
   value must block, not be ignored) — this is already the doctrine of
   the existing comment at lines 526-528 ("Resolution failure fails
   closed: session yes, clipboard no"), apply it identically here
   rather than inventing another behavior.
4. **Check/adjust `EndSession`/reconnect**: confirm no other path
   recomputes a grant from the policy alone without reapplying this
   clamp (`grep -rn resolveClipboardGrant` to be exhaustive — as of
   today only the two call sites listed exist, but reconfirm after
   your change).

## Constraints

- Don't touch `ClipboardFilter`/`clipboard.go` (the filtering
  mechanism itself is correct and already tested) — the bug is
  upstream, in the RESOLUTION of the grant passed to it, not in its
  enforcement.
- Don't turn `disable-copy`/`disable-paste` into a mechanism that
  could RELAX the policy: these params can only restrict further,
  never override a `WorkspacePolicy` denial. If you're unsure about an
  edge case, the most useful regression test is: policy denies + param
  absent/false → still denied; policy allows + param `true` → denied;
  policy denies + param `true` → denied; policy allows + param
  absent/false → still allowed (current behavior unchanged).
- `resolveClipboardGrant` must remain usable without params in
  contexts where they aren't relevant (keep a signature that doesn't
  break existing callers if you add a parameter — check all call
  sites before changing the signature).
- Don't re-document `params.go:122-129` unless the text becomes
  factually wrong after your fix (it should instead become true).

## Tests

- `api-server/internal/service/workspace_service_test.go` (or
  equivalent file): the cross policy×param cases listed above, for
  `Connect` AND for the remote-workspace equivalent, specifically
  covering the case "`disable-copy` only in the template, no override
  at connection time" (the trap from point 2).
- Check that `ConnectResult.Capabilities` AND the grant signed into
  the token carry the SAME clamped value (no divergence between what
  wwt enforces and what the menu shows — this is exactly the original
  bug).
- If the dev environment is available, an e2e check on an RDP
  workspace with `disable-copy: true` in the connection settings: the
  session menu must show "copy from workspace: blocked" AND an actual
  remote→host copy must fail at the proxy level (not just at the
  FreeRDP level) — useful to confirm that the two mechanisms (native
  guacd + wwt) are now consistent rather than redundant-by-luck. Cf.
  [[waas-guacd-clipboard-fix13]] for the clipboard verification method
  in dev (HTTPS :8443 required).
- `go build ./...` + `go test ./api-server/...`.

## Open points (your call)

- Location of the params→override conversion function
  (`workspace_service.go` local vs shared `params` package) — both
  work, choose whichever feels closest to the usage (the `params`
  package today has no value-reading logic, only registry/validation;
  that can be a reason to keep it on the service side). arbitrated on
  the service side
- If you factor out the template-params + session-params merge
  (already duplicated between `ConnectionInfo` and the new need in
  `Connect`), a small shared function is welcome but is not the main
  goal of this fix — don't launch into a refactor bigger than
  necessary., if the refactor isn't huge and is approachable via a
  small function it's a good idea to do it now
