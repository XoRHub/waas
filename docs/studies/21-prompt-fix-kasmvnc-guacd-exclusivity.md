# Prompt Fable 5 — Fix: the `kasmvnc` protocol must be exclusive (rejected if combined with vnc/rdp/ssh)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context: a choice left without an explicit decision

`WorkspaceProtocol.Name` accepts `vnc`, `rdp`, `ssh` (brokered by
guacd) and `kasmvnc` (kasmweb/*'s native web endpoint, reverse-proxied
by wwt, guacd out of the picture) —
`operator/api/v1alpha1/workspacetemplate_types.go:207-213`. A
`WorkspaceTemplate.spec.protocols` is a **list**: nothing today
prevents an admin from declaring `kasmvnc` alongside `vnc`/`rdp`/`ssh`
on the same template.

A previous iteration (desktop credential generation) discovered that
this case posed a concrete problem: the two generated-password
mechanisms (`kasmPasswordGenerated`,
`operator/internal/controller/kasm_credentials.go:49`, and
`desktopPasswordGenerated`,
`operator/internal/controller/desktop_credentials.go:44`) both name
their pod-namespace copy `computeName(ws)` (mandatory so the teardown
sweep can find it by name) and both inject `VNC_PW`. A `kasmvnc` +
`vnc` template would therefore have produced two colliding Secrets.
The fix at the time was to make the two mechanisms **mutually
exclusive at runtime**: `desktopPasswordGenerated` yields if
`kasmPasswordGenerated` returns true (kasm wins) — tested by
`TestDesktopCredentialsYieldToKasm`
(`operator/internal/controller/desktop_credentials_test.go:153`).

**That was a patch, not a product decision.** The repo still accepts,
at admission, a template that mixes `kasmvnc` with a guacd protocol —
a case that never makes functional sense (two radically different
connection mechanisms on the same desktop, only one of which can
actually win the password battle). The product decision, now settled:
**`kasmvnc` is exclusive**. A template declaring it may declare no
other protocol (`vnc`, `rdp`, `ssh`). `vnc`/`rdp`/`ssh` remain freely
combinable with each other, as today (e.g. `ubuntu-xfce`, commented in
`desktop_credentials_test.go:14`: "serves vnc AND rdp").

## What needs to be delivered

1. **Webhook** (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go`,
   function `validate`, around the `for i := range
   tpl.Spec.Protocols` loop that already feeds `seen map[string]bool`):
   after the loop (once `seen` is complete), if `seen["kasmvnc"]` is
   true and `seen` contains at least one other name, reject the
   creation/update with an explicit message, e.g.:
   `protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses
   guacd and must be the template's only protocol`. Follow the style
   of existing refusals (`v.deny(tpl, fmt.Sprintf(...))`, cf. the other
   `return nil, v.deny(...)` calls in the same function) rather than
   introducing a new message scheme. This applies to both
   `ValidateCreate` and `ValidateUpdate` (both already call `validate`).
2. **Type GoDoc** (`workspacetemplate_types.go:207-213`, the
   `WorkspaceProtocol.Name` comment): add a sentence documenting the
   exclusivity (e.g. "kasmvnc is exclusive: a template declaring it
   may declare no other protocol, admission-enforced"). Regenerate the
   manifests derived from the GoDoc if your tooling allows
   (`make manifests`/`make generate` depending on what the Makefile
   exposes — check that it does update `helm/waas/crds/...` and
   `operator/config/crd/bases/...`, already modified in the current
   working tree by other in-progress changes; don't blindly overwrite
   them, regenerate on top).
3. **User doc** (`docs/templates-and-protocols.md:38-63`, "Protocols"
   section): the current sentence ("A template may declare several
   protocols in guacd terms") says nothing about exclusivity. Add a
   clear line: `vnc`/`rdp`/`ssh` are freely combinable with each
   other, `kasmvnc` cannot coexist with any other protocol (rejected
   at admission).
4. **Clean up the runtime comment, without removing the safeguard**:
   the comments in `kasm_credentials.go` and `desktop_credentials.go`
   explain the mutual exclusion as if it were the only existing
   protection ("Mutually exclusive with the kasm mechanism: both
   inject VNC_PW and share the pod-copy Secret name, so only one may
   generate."). Update these comments to reference the new webhook
   guard as the primary protection, and explain why the runtime guard
   is still needed despite that (point 5 right below) — don't remove
   it. `TestDesktopCredentialsYieldToKasm` must continue to pass
   as-is: it tests a pure Go function, independent of the webhook.

## Constraints

- **The admission webhook only protects creations/updates, not objects
  already in storage.** An existing `WorkspaceTemplate` that already
  combines `kasmvnc` and `vnc`/`rdp`/`ssh` (if any exist) won't be
  rejected retroactively — it will keep going through the controller
  as-is until its next write (`ValidateUpdate` will then apply and
  block it, unless it becomes compliant again). **This is precisely
  why the runtime guard (`desktopPasswordGenerated` yields to
  `kasmPasswordGenerated`) must stay in place**: defense-in-depth for
  this grandfathering case, not dead code. Don't remove it on the
  grounds that the webhook "normally" makes the combination
  impossible.
- Don't touch the rest of `validate()` (params registry, audio port,
  `kasmvncConfig`, schedule, placement, workload labels) — out of
  scope here.
- `vnc` + `rdp` + `ssh` combined together (without `kasmvnc`) must
  remain accepted unchanged — check that no existing test covering
  this case breaks.
- Check `hack/dev/templates-dev.yaml` and any other YAML fixture in
  the repo (`grep -rn "name: kasmvnc" -A2` around each `protocols:`):
  none should combine `kasmvnc` with a guacd protocol today (verified
  ahead of this prompt, but reconfirm after your change — a fixture
  breaking CI would be a signal something slipped past you).

## Tests

- `operator/internal/webhook/v1alpha1/workspacetemplate_webhook_test.go`:
  extend `TestTemplateWebhookValidatesParamsAgainstRegistry` (or a new
  dedicated test function if you prefer to isolate this case) with:
  - `kasmvnc` + `vnc` → rejected;
  - `kasmvnc` + `rdp` → rejected;
  - `kasmvnc` + `ssh` → rejected;
  - `kasmvnc` + `vnc` + `rdp` + `ssh` (all three at once) →
    rejected, message clearly mentioning `kasmvnc`;
  - `kasmvnc` alone → still accepted (already covered by the existing
    "clean kasmvnc" case, `workspacetemplate_webhook_test.go:92` —
    just check it still passes);
  - `vnc` + `rdp` + `ssh` without `kasmvnc` → still accepted (add the
    case if it doesn't already exist).
- `operator/test/envtest/webhook_admission_test.go`: if this file
  already runs an end-to-end admission scenario on `kasmvnc`
  templates, add the combination rejection case there (otherwise, the
  unit coverage above is enough — don't add an envtest just to
  duplicate the unit test).
- `go build ./...` + `go test ./operator/...`.

## Open points (your judgment call)

- Exact location of the new check inside `validate()` (inside the main
  loop vs. right after, on `seen`) — both work, pick whichever reads
  best next to the existing `defaults > 1` check right after the loop.
- If you add a CRD regeneration (`make manifests`/`make generate`) and
  it touches files already modified in the working tree
  (`helm/waas/crds/...`,
  `operator/config/crd/bases/waas.xorhub.io_workspacetemplates.yaml`)
  for other reasons in progress, document this clearly in the commit
  to avoid any confusion about the diff's origin.
