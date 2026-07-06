# Diagnostic — lenteur d'affichage de la liste des workspaces

Constat utilisateur : la liste met longtemps à s'afficher, sans feedback.
Le feedback est traité (skeleton loaders + états vide/erreur distincts sur
`PortalPage`) ; ce document trace les causes probables de la latence côté
plateforme, pour une itération dédiée.

## Ce que fait réellement un affichage du portail

1. `GET /api/v1/workspaces` — deux LIST Kubernetes (workspaces + templates,
   pour enrichir chaque ligne de ses protocoles). Coût raisonnable et
   constant : **pas** de N+1 ici.
2. `GET /api/v1/me/quota` — c'est le point chaud :
   - `usageOf()` fait un `GET` **par workspace** du template correspondant
     (`internal/service/governance_service.go`) → N+1 sur l'API server
     Kubernetes dès que l'utilisateur (ou l'admin, qui voit tout) possède
     beaucoup de workspaces ;
   - le frontend **re-poll ce endpoint toutes les 15 s**
     (`useQuota → refetchInterval: 15000`), donc le N+1 tourne en boucle.
3. `GET /api/v1/catalog` + `GET /api/v1/workspace-templates` en parallèle
   à l'ouverture du dialogue de création — négligeable.

Autre facteur structurel : l'api-server parle à l'API Kubernetes **sans
cache** (client direct, pas d'informer). Chaque requête portail = plusieurs
allers-retours vers kube-apiserver ; sur un control-plane chargé, la
latence est celle de kube-apiserver, pas de la base.

## Pistes (non implémentées dans cette itération)

- **Supprimer le N+1 de `usageOf`** : un seul LIST des templates puis
  lookup en map (le même pattern existe déjà dans `WorkspaceService.List`).
  Gain immédiat, changement trivial — recommandé en premier.
- **Client Kubernetes avec cache** (informers/controller-runtime cache)
  pour workspaces/templates/images/policies : transforme les LIST répétés
  en lectures mémoire ; demande de gérer le démarrage (sync initiale).
- **Allonger/conditionner le poll quota** : ne re-fetcher le quota que sur
  mutation (create/pause/delete invalident déjà la query) + un intervalle
  long (60 s) en veille.
- **Pagination** de `/api/v1/workspaces` pour le cas admin (fleet) — le
  portail utilisateur n'en a pas besoin à court terme.
