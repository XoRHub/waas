# Volumes home : rétention, réutilisation, quotas

## Modèle

**Le PVC est la source de vérité** — pas de table SQL, pas de CRD dédié.
Un « volume conservé » est un PVC home géré par WaaS dont le workspace
n'existe plus, identifié par ses labels :

| Clé | Rôle |
|---|---|
| `app.kubernetes.io/managed-by: waas-operator` | objet géré par la plateforme |
| `waas.xorhub.io/owner: <uuid>` | propriété de l'utilisateur (clé quota + dashboards) |
| `waas.xorhub.io/retained: "true"` | détaché d'un workspace supprimé |
| annotation `waas.xorhub.io/origin-workspace` | provenance (nom d'affichage) |
| annotation `waas.xorhub.io/retained-at` | date de détachement (RFC3339) |

Les labels par-workspace (`waas.xorhub.io/workspace`, `…/workspace-namespace`)
sont retirés au détachement : ils pointeraient sur un CR mort.

## Cycle de vie

```
création ──► home PVC "<workload>-home" (labels live)
   │
suppression du workspace (DELETE /workspaces/{id}?keepVolume=…)
   │
   ├── keepVolume=true (DÉFAUT) ── finalizer : DÉTACHE
   │     retained=true + provenance ; le volume reste la propriété de
   │     l'utilisateur et CONTINUE de compter dans son quota storage
   │     │
   │     ├── réutilisation : création avec spec.homeVolumeName
   │     │     (« partir d'un volume existant ») — le webhook vérifie :
   │     │     même owner, volume retained, même namespace de destination
   │     │     (un PVC est namespacé : il n'est réattachable que là où il
   │     │     a été laissé). L'operator ré-étiquette le volume live.
   │     │
   │     └── suppression : dashboard utilisateur (onglet Volumes) ou
   │           vue admin (Fleet → Volumes) — auditée (volume.deleted,
   │           via=admin pour l'admin), jamais sans confirmation.
   │
   └── keepVolume=false (opt-in EXPLICITE du dialogue) ── finalizer :
         supprime le PVC avec le workspace. L'annotation
         waas.xorhub.io/delete-home="true" est le seul chemin de
         suppression avec le workspace : sans elle, on conserve.
```

Exception assumée : `lifecycle.maxLifetime` (policy) supprime le
workspace **et** son volume à l'expiration — récupérer le stockage est
précisément le contrat d'un TTL (comportement inchangé, documenté dans
`docs/governance.md`).

`cleanup: DeleteWhenEmpty` (placement) reste compatible : un namespace
qui héberge un volume conservé n'est jamais supprimé (le PVC waas le
retient). C'est le **namespace janitor** (reconciler interne de
l'operator) qui réclame le namespace quand le volume est finalement
supprimé — l'événement de suppression du PVC le re-déclenche, il n'y a
pas besoin qu'un workspace existe encore (voir
`docs/workspace-deletion.md`).

## Quotas

Les volumes conservés pèsent dans `limits.aggregate.storage` de la policy
**exactement comme côté admission** : le webhook, le re-check du
reconciler et `GET /me/quota` passent tous par `policy.RetainedVolumeLoads`
(charges storage-only, `Detached=true` — jamais comptées dans
`maxWorkspaces` ni dans le compute). La home affiche `used.storage /
limits.storage` du serveur, avec le détail « dont X conservés ». Un
volume adopté à la création est décompté à sa taille réelle (celle du
PVC, pas le homeSize du template).

## API

- `DELETE /api/v1/workspaces/{id}?keepVolume=true|false` (absent = true)
- `GET /api/v1/volumes` / `DELETE /api/v1/volumes/{ns}/{name}` (owner)
- `GET /api/v1/admin/volumes` / `DELETE /api/v1/admin/volumes/{ns}/{name}`
- `POST /api/v1/workspaces` accepte `homeVolumeName`

RBAC : l'operator détache/adopte (update PVC) ; l'api-server liste et
supprime (ClusterRole `…-api-server-volumes`, get/list/delete uniquement).

## Migration des volumes antérieurs

Les home PVC laissés par des workspaces supprimés AVANT cette
fonctionnalité n'ont pas le label `retained` : invisibles des dashboards
et du quota. Pour les intégrer :

```sh
kubectl label pvc <name> -n <ns> waas.xorhub.io/retained=true
kubectl label pvc <name> -n <ns> waas.xorhub.io/workspace- waas.xorhub.io/workspace-namespace-
kubectl annotate pvc <name> -n <ns> waas.xorhub.io/retained-at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
```

(le label `waas.xorhub.io/owner` existant fait foi pour la propriété).
