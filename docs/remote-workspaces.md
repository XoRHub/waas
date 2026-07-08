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
  l'IdP) ; les admins plateforme passent toujours.
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

## Wake-on-LAN (relais externe)

Une machine remote peut être allumée par Wake-on-LAN si une **adresse
MAC** est renseignée (`macAddress`, validée et normalisée en
`aa:bb:cc:dd:ee:ff` par l'api-server).

**D'où part le magic packet.** Un pod du cluster ne peut pas broadcaster
sur le L2 physique des machines cibles. L'émission est donc **déléguée à
un relais externe** sur le LAN de la cible (équipement manageable,
routeur WoL, ou petit agent), que l'api-server déclenche en HTTP :

```
POST $WAAS_WOL_RELAY_URL   {"mac": "aa:bb:cc:dd:ee:ff"}
Authorization: Bearer $WAAS_WOL_RELAY_TOKEN   # si défini
```

Config api-server : `WAAS_WOL_RELAY_URL` (active la feature),
`WAAS_WOL_RELAY_TOKEN` (optionnel). Sans URL, le réveil renvoie
`503 Unavailable` et le bouton Wake reste sans effet.

**Limite réseau (à documenter pour l'exploitant).** Le magic packet
n'atteint sa cible que si le relais est sur le **même domaine L2** que la
machine. En multi-site, prévoir **un relais par site/VLAN** ; le mapping
machine → relais est aujourd'hui global (un seul relais) — pour du
multi-site, router côté relais (par sous-réseau) ou étendre le modèle
avec un sélecteur de site sur le RemoteWorkspace.

**Flux.** Bouton « Réveiller » manuel sur la card (dès qu'une MAC est
renseignée). À l'ouverture (open-desktop) d'un remote avec MAC : si la
connexion guacd échoue (machine éteinte), l'UI tente automatiquement un
WoL une fois, laisse ~20 s à la machine pour démarrer, puis réessaie la
connexion — un magic packet vers une machine déjà allumée est sans effet,
donc l'opération est idempotente. Audit : `remote_workspace.woke`.

## Réseau & RBAC (à prévoir côté plateforme)

- guacd doit pouvoir **sortir** vers les machines cibles : adapter les
  NetworkPolicies (l'exemple `waas-images/examples/networkpolicy-workspaces.yaml`
  ne couvre que le trafic in-cluster).
- Le Role de l'api-server a gagné `create/update/delete` sur les Secrets
  du namespace workspaces (toujours sans `list`/`watch`) — voir
  `helm/waas/templates/api-server.yaml`.
- Les clipboard policies (`WorkspacePolicy.spec.clipboard`) s'appliquent
  aussi aux sessions remote (même token, même filtre wwt).
