# Remote Workspaces — machines hors cluster via guacd

Fonctionnalité distincte du flux "New workspace" : un utilisateur autorisé
enregistre des machines **extérieures au cluster** (hôte, port, protocole
ssh/vnc/rdp, identifiants) et s'y connecte via la même chaîne
frontend → wwt → guacd que les workspaces provisionnés.

## Modèle de données (délibérément séparé)

| Aspect | Workspace provisionné | Remote workspace |
|---|---|---|
| Entité | CR `Workspace` (+ opérateur) | ligne SQL `remote_workspaces` |
| Cycle de vie | provisioning, pause, TTL, PVC | aucun — la machine est gérée ailleurs |
| Cible réseau | Service in-cluster (`status.address`) | `hostname:port` fourni par l'utilisateur |
| Credentials | Secret du template (`credentialsSecretRef`) | **un Secret Kubernetes par entrée** (`waas-remote-<id>`) |
| Suppression | teardown opérateur | suppression ligne + Secret, rien d'autre |

Les identifiants (`username`, `password`, `private-key`, `passphrase`)
sont **write-only** : envoyés à la création/édition, stockés uniquement
dans le Secret, jamais renvoyés par l'API (le modèle n'expose que
`credentialKeys`, la liste des clés présentes). L'api-server les résout
au connect (endpoint interne `/internal/v1/sessions/{id}/connection`,
inaccessible hors cluster) — même flux que les templates.

## Contrôle d'accès

Opt-in par policy, fail-closed :

```yaml
# WorkspacePolicy
spec:
  remoteWorkspaces: true   # absent/false = fonctionnalité invisible et refusée
```

- Résolution identique au reste de la gouvernance (priorité, groupes
  Authentik) ; les admins plateforme passent toujours.
- Le flag est projeté vers le portail via `GET /api/v1/me/quota`
  (`features.remoteWorkspaces`) : l'onglet n'existe pas pour les autres.
- Chaque entrée appartient strictement à son créateur — même un admin ne
  voit pas les remotes (ni les credentials) d'un autre utilisateur.

## Paramètres guacd

Le formulaire réutilise le registre déclaratif de la plateforme
(`operator/pkg/params`, servi par `GET /api/v1/meta/protocols`) : mode
simple (tier `ui`) par défaut, case "paramètres avancés" pour le tier
`advanced`. Les paramètres platform-owned (hostname, port, credentials,
gateways, enregistrement…) sont refusés par la même validation que les
templates — à l'enregistrement ET au connect.

## API

```
GET    /api/v1/remote-workspaces            # ses propres entrées
POST   /api/v1/remote-workspaces            # {name, hostname, port, protocol, params?, credentials?}
GET    /api/v1/remote-workspaces/{id}
PUT    /api/v1/remote-workspaces/{id}       # credentials: champ absent = conservé, "" = supprimé
DELETE /api/v1/remote-workspaces/{id}       # supprime aussi le Secret
POST   /api/v1/remote-workspaces/{id}/connect  # → {sessionId, connectionToken, …}
```

Les sessions portent `kind = "remote"` (colonne `sessions.kind`,
migration `20260707100001`) ; audit : `remote_workspace.created/updated/
deleted` + `session.started` (cible incluse, jamais les credentials).

## Réseau & RBAC (à prévoir côté plateforme)

- guacd doit pouvoir **sortir** vers les machines cibles : adapter les
  NetworkPolicies (l'exemple `waas-images/examples/networkpolicy-workspaces.yaml`
  ne couvre que le trafic in-cluster).
- Le Role de l'api-server a gagné `create/update/delete` sur les Secrets
  du namespace workspaces (toujours sans `list`/`watch`) — voir
  `helm/waas/templates/api-server.yaml`.
- Les clipboard policies (`WorkspacePolicy.spec.clipboard`) s'appliquent
  aussi aux sessions remote (même token, même filtre wwt).
