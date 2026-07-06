# Workspace templates: workloads, protocols and user overrides

This document covers the template-driven deployment model introduced after
the governance layer: how a `WorkspaceTemplate` shapes the workload, how
multiple connection protocols are declared and tuned, and which parts a
workspace creator may override.

## Workload

Linux desktops are no longer bare pods. The template picks the workload
kind and passes through the pod spec:

```yaml
spec:
  workload:
    kind: Deployment          # Deployment (default) | StatefulSet | Pod
    securityContext: {...}     # container-level
    podSecurityContext: {...}  # pod-level
    volumes: [...]
    volumeMounts: [...]
    nodeSelector: {...}
    tolerations: [...]
    serviceAccountName: ...
```

- **Deployment** (default): replicas=1, `Recreate` strategy — the home PVC
  is RWO, two desktop pods must never overlap.
- **StatefulSet**: stable identity; `serviceName` is the workspace service.
- **Pod**: the legacy bare-pod behavior.

The home PVC mount, protocol ports and probes stay platform-managed and
cannot be overridden. Windows/KubeVirt is unchanged.

Changing the workload kind of a template does not touch running
workspaces (grandfathering counts any existing kind); the new kind applies
on the next provisioning (resume after pause, or recreate).

## Protocols

A template may declare several protocols in guacd terms:

```yaml
spec:
  protocols:
    - name: vnc          # vnc | rdp | ssh
      port: 5901
      default: true      # first entry wins if none is marked
      params:            # locked guacd connection parameters
        color-depth: "24"
      userParams: [color-depth, cursor]   # user-tunable at connect time
```

When `protocols` is empty, one protocol is synthesized from `os`/`port`
(linux → vnc:5901, windows → rdp:3389) so older templates keep working.

- The workspace **Service exposes every declared port**; status carries
  the full list (`status.protocols`) plus the effective default.
- The catalog gate checks **every** declared protocol against the
  `WorkspaceImage.protocols` list (`ssh` is now a valid image protocol).
- `POST /api/v1/workspaces/{id}/connect` accepts an optional body
  `{"protocol": "rdp", "params": {"color-depth": "16"}}`. The api-server
  rejects protocols the template does not declare and, for non-admins,
  any param name outside `userParams`. Accepted params are stored on the
  session and merged over the template params in the internal
  ConnectionInfo; wwt forwards them during the guacd handshake.
  `hostname`/`port` remain platform-managed whatever the params say.
- The portal stores each user's per-workspace protocol/params choice in
  their profile preferences (`workspaceSettings`) — the server still
  re-validates at connect time, so the preference is a convenience, not a
  grant.

## Creator overrides

The template decides what workspace creators may deviate:

```yaml
spec:
  overrides:
    allowedFields: [env, resources, protocol, protocolParams,
                    securityContext, podSecurityContext, volumes,
                    nodeSelector, tolerations]
    owner: alice        # this platform user may override anything
```

`Workspace.spec.overrides` mirrors the workload passthrough (env,
security contexts, volumes/mounts, nodeSelector, tolerations, protocol).
Merge semantics: env/volumes/mounts merge by name (workspace wins),
nodeSelector merges key-wise, tolerations append, security contexts
replace.

Enforcement is the usual two-line defense:

- the **admission webhook** denies `[OverrideNotAllowed]` when a set field
  is not in `allowedFields` — unless the creator is a platform admin
  (`waas.xorhub.io/role: admin` annotation, trusted-writer only, frozen
  like the other identity annotations) or the template `overrides.owner`;
- the **reconciler** re-checks before creating compute (deferred-template
  case).

Note: allow-listing `volumes` lets users mount arbitrary volume sources —
including hostPath. Only enable it on templates aimed at trusted groups.

## Portal UX shipped alongside

- **Split view** (`/view`): 1–3 desktops side by side, Termix-style
  dynamic splits (split right/down per pane, draggable dividers, per-pane
  keyboard focus and rescaling).
- **Folders**: users group their workspaces into named boxes (stored in
  profile preferences, purely presentational).
- **Theme**: light/dark/system, persisted in the profile, quick toggle in
  the avatar menu.
