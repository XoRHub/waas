# Diagnostic — pause/resume ne scale pas les workloads

Constat utilisateur : mettre un workspace en pause (ou le reprendre) ne
scale pas le pod à 0/1. Les crons d'uptime/downtime étaient suspectés du
même mal — à raison : ils passent par le même mécanisme.

## Chaîne vérifiée

1. **UI → api-server** : `POST /pause|/resume` → `SetPaused`
   (`api-server/internal/service/workspace_service.go`) écrit bien
   `spec.paused` + l'annotation `waas.xorhub.io/manual-state-at` sur le CR.
   Le Role namespace de l'api-server a `update` sur `workspaces`. ✅
2. **Déclenchement du reconcile** : `spec.paused` change → la génération
   du CR est incrémentée → le `GenerationChangedPredicate` laisse passer
   l'événement. ✅
3. **Logique de scale** : `ensureDeployment`/`ensureStatefulSet`
   (`operator/internal/controller/workload.go`) calculent
   `spec.replicas = 0|1` et appellent `r.Update(ctx, existing)`. La logique
   est correcte. ✅
4. **RBAC de l'operator** : ❌ **cause racine**. Le ClusterRole (généré
   *et* chart Helm) n'accordait que
   `create, delete, get, list, watch` sur `deployments`/`statefulsets`
   (idem `virtualmachines` pour le toggle `spec.running` des VM Windows).
   Le `r.Update()` du scale était rejeté **Forbidden** ; le reconcile
   sortait en erreur avant `patchStatus`, donc retry en backoff à l'infini
   et statut affiché inchangé.

Les crons d'uptime/downtime aboutissent au même `ensureWorkload(down)` :
même verbe manquant, même panne. Un seul correctif couvre les deux bugs.

## Pourquoi les tests ne l'ont pas vu

Les tests du controller utilisent le client *fake* de controller-runtime,
qui n'applique aucun RBAC : `TestReconcilePausedScalesToZero…` passait
alors que le cluster réel refusait l'update. Le chart Helm reproduit à la
main les marqueurs kubebuilder — aucune vérification ne liait les deux.

## Correctif

- `update` ajouté aux marqueurs `+kubebuilder:rbac` sur
  `deployments;statefulsets` et `virtualmachines`
  (`workspace_controller.go`), `config/rbac/role.yaml` régénéré
  (`make manifests`), ClusterRole du chart aligné
  (`helm/waas/templates/operator.yaml`).
- **Garde anti-régression** (`internal/controller/rbac_test.go`) :
  - chaque `(group, resource, verb)` du role généré doit être couvert par
    le ClusterRole du chart — le miroir manuel ne peut plus dériver ;
  - `update` sur les trois kinds de workload est vérifié explicitement.
- **Preuve du scale par les crons**
  (`internal/controller/workspace_schedule_test.go`, horloge injectée via
  `WorkspaceReconciler.Now`) : edge downtime → replicas 0 + phase
  `Stopped` + requeue exactement à l'edge suivant ; edge uptime →
  replicas 1 ; **tick raté** (controller éteint à l'heure de l'edge)
  rattrapé au reconcile suivant — l'état dérive du dernier edge, pas de
  l'observation du tick ; pause manuelle pendant une plage d'uptime
  (règle B : elle gagne jusqu'au prochain edge opposé) ; resume manuel en
  plage de downtime ; timezone du schedule respectée quelle que soit
  l'horloge du controller ; override de schedule prioritaire sur le
  template.

## À déployer

Le fix est purement RBAC : `helm upgrade` (ou sync ArgoCD) suffit, aucun
redémarrage de workspace nécessaire. Les workspaces coincés en
pause/reprise convergent au premier reconcile après l'upgrade.
