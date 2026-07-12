# Clipboard mapping: current precedence order (analysis, not a prompt)

Analysis document, not an implementation prompt — goal: faithfully
document the REAL state of the code (verified file by file, line by
line) to enable a decision on "does `WorkspacePolicy.spec.clipboard`,
`WorkspaceTemplate.spec.protocols[].params`, `.userParams`, and the
session menu duplicate each other". Verdict at the end, but read the
mapping first: the answer isn't the same depending on the protocol
family.

## Central finding: TWO disjoint mechanisms, not one

The clipboard is not governed by ONE resolution path, but by TWO, with
no shared code beyond `WorkspacePolicy.spec.clipboard` and
`policy.ClipboardOf()`:

- **guacd (`vnc`/`rdp`/`ssh`)**: 4 layers (policy, template `params`,
  template `userParams`, connection override), resolved on EVERY
  connection, enforced by the wwt proxy via the connection token.
- **`kasmvnc`**: a single layer (policy), resolved at RECONCILE time
  (not at connection time), baked into `~/.vnc/kasmvnc.yaml` by the
  operator. Template `params`/`userParams` have **no effect at all** —
  verified: `disable-copy`/`disable-paste` are only registered for
  `vnc`/`rdp`/`ssh` (`operator/pkg/params/params.go:122-130`,
  `Protocols: []string{"vnc", "rdp", "ssh"}`), and the template
  webhook (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go:68-69`,
  `params.ValidateUserParamNames`) rejects, at creation time, a
  `userParams` citing these names on a `kasmvnc` entry — which is
  anyway impossible to combine with `vnc`/`rdp`/`ssh` on the same
  template (L93-97, `kasmvnc` is exclusive). So this is not a silent
  trap: the webhook already prevents the inconsistent configuration.

## Mapping of configuration points

| Field | Who edits it | Protocol scope | When evaluated | Real role |
|---|---|---|---|---|
| `WorkspacePolicy.spec.clipboard` (`CopyFromWorkspace`/`PasteToWorkspace`) | admin, CR | all | on every connection (guacd) / on every reconcile (kasmvnc) | **security ceiling, sole authority for kasmvnc** |
| `WorkspaceTemplate.spec.protocols[].params["disable-copy"/"disable-paste"]` | admin, template CR | `vnc`/`rdp`/`ssh` only | on every connection, template refetched | default value applied if the user doesn't submit an override |
| `WorkspaceTemplate.spec.protocols[].userParams` | admin, template CR | `vnc`/`rdp`/`ssh` only | on every connection | **delegation of NAMES**, not values: which parameters the user is allowed to submit as an override |
| `ConnectInput.Params` ("connection settings" dialog, `ConnectionSettingsDialog.tsx`) | connected user (or template owner / admin) | `vnc`/`rdp`/`ssh` only, delegated names only | on every connection, ephemeral — never persisted on a CR | effective value requested for THIS session |
| Session menu (`SessionOverlay.tsx`, in-session overlay) | read-only | all | display only | mirrors the already-clamped result + explains WHO blocked it (`ClipboardLockPolicy` vs `ClipboardLockParams`) |

The "session menu" **is never a decision point** — it's a mirror of
the result already computed server-side (`clipboardCapabilities`,
`api-server/internal/service/workspace_service.go:632-656`). So there
aren't 3 authorities that could contradict each other, only 1 ceiling
(policy) and cascading restrictions.

## Exact resolution chain — guacd (`vnc`/`rdp`/`ssh`)

Code: `WorkspaceService.Connect`, `workspace_service.go:396-515`,
`clampClipboardGrant`/`mergeParams`/`clipboardCapabilities` L609-656.

```
1. policyGrant = policy.ClipboardOf(resolved policy of the CONNECTED user)
   → resolution failed (no user, no matched policy) = fail
     closed, (false, false)

2. For each direction (copy / paste), effective param value:
   - if the user submitted a value in ConnectInput.Params
     AND this name is authorized
       (name ∈ params.ResolveUserParamNames(protocol, entry.UserParams)
        OR actor == admin OR actor == declared owner of the template)
       AND the connected user's policy allows the FieldProtocolParams field
       (intersection template.Overrides.AllowedFields × policy.Overrides.AllowedFields)
     → this value is used (mergeParams: override OVERWRITES the default)
   - otherwise, the template's value (entry.Spec.Params[name]) if present
   - otherwise, absent (no restriction from this layer)

3. effectiveGrant.direction = policyGrant.direction
                               AND NOT(effective value == true)
   → a param can ONLY restrict, never widen beyond the
     policy (test "false params never override a policy denial",
     connect_clipboard_test.go)

4. The signed connection token embeds effectiveGrant — that's what
   wwt enforces via the tunnel, not the capabilities.
   clipboardCapabilities(policyGrant, effectiveGrant) builds
   ONLY the display view (session menu) + the blocking-reason label
   (policy wins the label if both block — removing the param would
   change nothing).
```

**Key point**: `params` (locked) and `userParams` (delegated) are
**not mutually exclusive in the schema** — a name can appear in both
at once (`params` supplies a default value, `userParams` allows the
user to change it for THEIR session). An admin who wants a real lock
(no negotiation possible) simply has to **omit** the name from
`userParams` — putting it in `params` alone is enough to fix the value
for everyone, without needing to list it anywhere else for it to be
"locked". This is not a bug, but the word "locked" in the code
comments can be confusing if you expect `params` and `userParams` to
be a mutually exclusive pair (like "either fixed, or delegated") —
that's not the actual model.

## Exact resolution chain — `kasmvnc`

Code: `WorkspaceReconciler.ensureKasmConfig`/`kasmClipboardGrant`/
`applyClipboardPolicy`, `operator/internal/controller/kasm_config.go:76-190`.

```
1. At RECONCILE time (not at connection time): policy.ClipboardOf(resolved
   policy of the workspace OWNER, not of the connected user — explicit
   comment: "Container-level DLP can only enforce ONE policy per
   workload, so it follows the owner").
   → resolution failed = fail closed, (false, false).

2. These two booleans are stamped into the effective kasmvnc.yaml
   (admin's kasmvncConfig + these keys last, so AUTHORITATIVE):
     data_loss_prevention.clipboard.server_to_client.enabled = copyAllowed
     data_loss_prevention.clipboard.client_to_server.enabled = pasteAllowed
     allow_client_to_override_kasm_server_settings = false (always,
       to prevent the KasmVNC client from reopening what's closed)

3. The template `params`/`userParams` NEVER come into play here — no
   additional restriction or delegation lever exists for kasmvnc, only
   the policy.

4. Separately, at connection time, the api-server computes
   `capabilities` for display (session menu) — but with the CONNECTED
   user's policy, not the owner's (`clipboardGrant` reuses the same
   `resolveClipboardGrant` function as the guacd path, with no
   owner/connected-user distinction).
```

**Watch point identified (documented in the code, not a secret, but
worth examining if you change the sharing model)**: on a SHARED
`kasmvnc` workspace (RW/RO — see
[[waas-admin-workspace-scope-fix15]]), the policy actually enforced
inside the container is the **owner's**, whereas what the guest user
sees in their session menu (`capabilities`) reflects THEIR OWN policy.
The code comment explicitly assumes this: "The two agree on personal
kasmvnc workspaces (owner == connecting user); the operator follows
the workspace owner because container-level DLP is one-per-workload."
On a share between two users with different policies, a guest's
session menu can therefore display a right that doesn't match what the
container actually enforces. Not an exploitation path (DLP remains
fail-closed on the container side, so it's never more permissive than
what's displayed in the worst case where the guest would have a
policy MORE permissive than the owner's — in that case the menu would
lie in the "more restrictive than reality" direction; the reverse —
optimistic menu, real DLP more restrictive — is the annoying case for
UX, not for security).

## Verdict

**Not a duplication**: in both families, `WorkspacePolicy.spec.clipboard`
remains the sole security authority. The template can never relax it
— on the guacd side it can only restrict it further (logical AND,
never OR), on the kasmvnc side it has no lever at all. There aren't 3
places that "decide" independently on the same thing: a single
ceiling, cascading restrictions, a webhook that already prevents the
inconsistent configuration (kasmvnc + clipboard userParams rejected at
creation).

**What deserves your judgment call, not a bug but a design choice**:
1. **Structural asymmetry, accepted as-is** between guacd (4
   negotiable layers) and kasmvnc (1 layer, all-or-nothing) —
   consistent with the fact that kasmvnc has no guacd tunnel to
   instrument directly, but it means an admin can NEVER delegate the
   clipboard to the user on a kasmvnc workspace, even partially,
   whereas they can on vnc/rdp/ssh.
2. **Menu/reality divergence on shared kasmvnc** (above) — to fix if
   RW/RO sharing of kasmvnc workspaces becomes a real usage pattern
   (today documented as acceptable because it's rare).
3. **Non-mutually-exclusive `params`+`userParams` semantics** — works
   as intended but the vocabulary ("locked") can mislead an admin who
   reads the schema without the code; worth clarifying in the CRD docs
   if you keep this model as-is.
