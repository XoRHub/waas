# Prompt Fable 5 — Fix : un admin ne doit voir/se connecter qu'à SES workspaces depuis « My Workspaces »

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo

Aujourd'hui, un admin connecté sur SA page personnelle « My Workspaces »
(pas la page fleet admin) voit et peut se connecter à **tous** les
workspaces du cluster, tous propriétaires confondus — c'est un problème
de design/sécurité (traçabilité). Le seul endroit où un admin doit
pouvoir _voir_ les workspaces des autres utilisateurs, sans jamais s'y
connecter, est la page Fleet admin (revue dans le commit précédent,
`docs/studies/12-prompt-fix-fleet-owner-grouping.md` : les 3 onglets
fleet groupent déjà par owner). Une future feature de partage RW/RO
initiée par le propriétaire (dépannage tracé) est **hors scope** ici —
mentionne-la seulement comme point ouvert, ne l'implémente pas.

- **Le bug est à la fois côté visibilité (List) et côté action
  (fetchByID)** :
  - `WorkspaceService.List` (`api-server/internal/service/workspace_service.go:126-166`)
    ne filtre jamais pour un admin :

    ```go
    func (s *WorkspaceService) List(ctx context.Context, actor Actor) ([]model.Workspace, error) {
        isAdmin := actor.Role == string(auth.RoleAdmin)
        opts := []client.ListOption{client.InNamespace(s.namespace)}
        if !isAdmin {
            opts = append(opts, client.MatchingLabels{ownerLabel: actor.ID})
        }
    ```

    C'est cette même liste (route unique `GET /api/v1/workspaces`,
    `router.go:74`, hook unique `useWorkspaces()`,
    `frontend/src/hooks/useApi.ts:34`) que consomment **à la fois** la
    page personnelle (`frontend/src/sections/WorkspacesSection.tsx`,
    montée par `PortalPage.tsx`) et `WorkspacesFleet()`
    (`frontend/src/pages/admin/FleetPage.tsx:90-92`). Rien ne distingue
    aujourd'hui les deux usages côté serveur.
  - `WorkspaceService.fetchByID` (`workspace_service.go:930-940`),
    utilisé par `Get`, `Delete`, `SetPaused`/`Resume`,
    `UpdateOverrides`, `Reload`, `EffectiveKasmVNCConfig`, `Events`,
    `Resize` **et** `Connect` (`workspace_service.go:543-547`), contient
    un bypass explicite pour l'admin :

    ```go
    if actor.Role != string(auth.RoleAdmin) && ws.Spec.Owner != actor.ID {
        // 404, not 403: don't leak the existence of other users' workspaces.
        return nil, apierror.NotFound("workspace not found")
    }
    ```

    Un admin peut donc appeler `POST /workspaces/{id}/connect` sur
    n'importe quel workspace du cluster, pas seulement les siens — c'est
    le cœur du bug.
- **Frontend** : `WorkspacesSection.tsx` rend `workspaces.data.data` tel
  quel dans `FolderedGrid` (groupage par _dossiers personnels_ de
  l'utilisateur, sans rapport avec `ownerId`), avec les boutons
  Open/Pause/Delete pleinement actifs. `WorkspaceCard` ouvre
  `target.connectUrl` = `` `/workspaces/${ws.id}/connect` ``
  (`frontend/src/lib/target.ts:73`) sans aucune condition d'ownership.
  Une fois `List` corrigé, un admin ne recevra plus que ses propres
  workspaces sur cette page — le filtrage doit donc être fait côté
  serveur, pas frontend.
- **Piège : `FleetPage.tsx` a besoin de continuer à voir et supprimer
  les workspaces des autres** — le commit précédent (étude 12) a
  volontairement câblé `remove.mutate` (`useDeleteWorkspace()`) pour
  fonctionner sur les workspaces de tous les owners depuis la fleet,
  avec un commentaire explicite :

  ```
  {/* Admin fleet delete always RETAINS the user's volume: ... */}
  onClick={() => remove.mutate({ id: ws.id, keepVolume: true })}
  ```

  Si tu retires bêtement le bypass de `fetchByID`, ce delete admin
  casse. Il faut donc **séparer** la liste et la suppression
  « fleet-wide » (légitimes, gestion d'infra) de l'accès « session
  live » (`Connect` et, par cohérence, les autres actions par ID —
  `Get`, pause, resize, overrides, reload, kasmvnc-config, events) qui
  doit devenir strictement réservé au propriétaire, sans exception de
  rôle.
- **Deux patterns sœurs existent déjà dans le repo pour exactement ce
  problème** — reprends-les, ne réinvente rien :
  - **Volumes** (le plus proche de ce qu'il faut faire ici) :
    `WorkspaceService.ListRetainedVolumes(ctx, actor, all bool)`
    (`volume_service.go:24-28`) — `all=false` filtre par owner,
    `all=true` ne filtre pas. Deux handlers distincts appellent la même
    méthode : `ListVolumes` (`workspace_handler.go:110-117`, route
    `GET /api/v1/volumes`, **toujours** `all=false`) et
    `AdminListVolumes` (`workspace_handler.go:130-137`, route
    `GET /api/v1/admin/volumes` sous `middleware.RequireAdmin`,
    `router.go:157-158`, **toujours** `all=true`). Idem côté suppression :
    `DeleteVolume` (route utilisateur, jamais admin) vs
    `AdminDeleteVolume` (route admin dédiée, `router.go:159`). Deux
    hooks frontend distincts : `useWorkspaces`-like perso vs
    `useAdminVolumes`/`useAdminDeleteVolume` (`useApi.ts:256,264`),
    consommés respectivement par la page perso et par
    `VolumesFleet()`.
  - **Remote workspaces** (le plus strict, mais sans suppression admin
    du tout — pas forcément ce qu'on veut ici puisque le delete fleet
    est déjà un acquis de l'étude 12) : `List` toujours scopé à
    `actor.ID` (`remote_workspace_service.go:255-260`), `AdminList`
    séparé sans filtre (`:460`), et surtout `fetchOwned`
    (`remote_workspace_service.go:536-548`) **sans aucun bypass rôle** :

    ```go
    // Ownership is strict — remotes and their credentials are personal.
    if rw.OwnerID != actor.ID {
        return nil, apierror.NotFound("remote workspace not found")
    }
    ```

    C'est le modèle à suivre pour `fetchByID` : retire le
    `actor.Role != admin &&`, ne garde que la comparaison d'ownership.

## Ce qu'il faut livrer

1. **`WorkspaceService.List`** : donne-lui un paramètre `all bool`
   (signature `List(ctx, actor, all bool)`, comme
   `ListRetainedVolumes`) ou toute variante équivalente que tu juges
   plus lisible (méthode séparée `AdminList`, à la
   `RemoteWorkspaceService`) — documente ton choix. Dans tous les cas :
   - la route personnelle `GET /api/v1/workspaces` (consommée par
     `WorkspacesSection`/`PortalPage`) doit **toujours** filtrer par
     `ownerLabel: actor.ID`, y compris pour un admin ;
   - ajoute une route admin dédiée `GET /api/v1/admin/workspaces`
     (nouveau bloc sous `router.go:150` aux côtés de
     `admin/volumes`/`admin/remote-workspaces`, sous
     `middleware.RequireAdmin`) qui renvoie tout le namespace, avec
     `OwnerUsername` résolu (reprends la logique déjà écrite en
     `workspace_service.go:146-163`, juste conditionnée à `all` plutôt
     qu'à `isAdmin`).
2. **`fetchByID`** (`workspace_service.go:930-940`) : retire le bypass
   de rôle, ne garde que `ws.Spec.Owner != actor.ID` → 404, exactement
   comme `RemoteWorkspaceService.fetchOwned`. Ça rend strictes toutes
   les actions qui passent par elle : `Connect`, `Get`, `SetPaused`,
   `UpdateOverrides`, `Reload`, `EffectiveKasmVNCConfig`, `Events`,
   `Resize` — un admin ne peut plus agir sur le workspace d'un autre
   utilisateur via ces endpoints, quel que soit le canal d'appel (UI ou
   appel direct à l'API).
3. **Suppression admin fleet** : comme `fetchByID` devient strict, le
   `Delete` via la route utilisateur (`DELETE /workspaces/{id}`) devient
   lui aussi strict — attendu, un utilisateur ne doit supprimer que les
   siens. Mais l'admin fleet doit continuer à pouvoir supprimer
   n'importe quel workspace (comportement acquis de l'étude 12, avec
   retain du volume). Ajoute donc, à l'image de
   `AdminDeleteVolume`/`admin/volumes/{namespace}/{name}` :
   - une méthode service séparée, p. ex.
     `AdminDelete(ctx, actor, id string, keepVolume bool)`, qui saute
     `fetchByID` et va chercher le workspace sans contrôle d'ownership
     (l'appelant est déjà garanti admin par le middleware de route) ;
   - une route `DELETE /api/v1/admin/workspaces/{id}` sous
     `middleware.RequireAdmin` (même bloc que le reste de `/admin`,
     `router.go:150-160`) ;
   - un hook frontend `useAdminDeleteWorkspace()` (mirroir de
     `useAdminDeleteVolume`, `useApi.ts:264`).
4. **Frontend `FleetPage.tsx`, `WorkspacesFleet()`
   (`FleetPage.tsx:90-...`)** : bascule de `useWorkspaces()` +
   `useDeleteWorkspace()` vers les nouveaux
   `useAdminWorkspaces()`/`useAdminDeleteWorkspace()` (mirroir exact de
   `useAdminVolumes`/`useAdminDeleteVolume`, déjà utilisés par
   `VolumesFleet()` dans le même fichier). Le groupage par owner
   (étude 12) ne change pas de logique, seule la source de données
   change de hook.
5. **`WorkspacesSection.tsx`/`PortalPage.tsx`** : aucun changement de
   code attendu — une fois `List` corrigé côté serveur, un admin n'y
   verra plus que ses propres workspaces automatiquement. Vérifie
   simplement qu'aucun filtre frontend redondant n'est nécessaire.
6. **Génère les types** : `make generate-types` si le schéma de
   réponse admin diffère (`OwnerUsername` existe déjà sur
   `model.Workspace`, donc a priori pas de nouveau type, seulement une
   nouvelle route dans le client généré si applicable).

## Contraintes

- Ne casse pas le groupage par owner de la fleet (étude 12) : la route
  admin doit continuer à renvoyer `OwnerUsername` peuplé pour chaque
  ligne.
- Ne touche pas à `RemoteWorkspaceService` ni `VolumesFleet()` — déjà
  conformes, hors scope de ce fix.
- N'implémente pas le partage RW/RO tracé par le propriétaire — note-le
  en point ouvert seulement.
- Le comportement de suppression fleet (retain du volume, résilience
  aux erreurs) doit rester identique à ce qu'a livré l'étude 12, juste
  via un chemin d'autorisation différent (route/méthode admin dédiée
  plutôt que bypass dans `fetchByID`).
- Documente explicitement, dans le commit, la liste des actions
  désormais strictement propriétaire-only (`Connect`, `Get`, pause,
  resize, overrides, reload, kasmvnc-config, events) vs celles qui
  restent admin-accessibles via un chemin dédié (`List` fleet, delete
  fleet) — c'est la distinction centrale de ce fix, ne la laisse pas
  implicite.

## Tests

- Go : nouveau test (ou extension de `workspace_service_test.go`)
  vérifiant qu'un actor admin appelant `Connect`/`Get`/`SetPaused`/etc.
  sur le workspace d'un autre utilisateur reçoit `404 NotFound` (comme
  un non-admin), alors qu'il continue de fonctionner sur son propre
  workspace. Étends/adapte `TestListResolvesOwnerUsernamesForAdmins`
  (`workspace_service_test.go:206-266`) : la liste **personnelle**
  (`all=false`) pour un actor admin ne doit renvoyer que ses propres
  workspaces ; un test séparé sur la liste **admin** (`all=true`)
  confirme qu'elle renvoie tout + `OwnerUsername`. Ajoute un test pour
  `AdminDelete` (fonctionne sur le workspace d'un autre user, retain du
  volume respecté).
- Vitest : mets à jour `FleetPage.test.tsx` (créé par l'étude 12) pour
  mocker les nouveaux hooks `useAdminWorkspaces`/`useAdminDeleteWorkspace`
  plutôt que `useWorkspaces`/`useDeleteWorkspace`.
- `go build ./...` + tests Go sur `api-server` ; `tsc -b` + vitest sur
  `frontend`.

## Points ouverts (ton arbitrage)

- Nom exact des nouveaux symboles serveur (`List(ctx, actor, all bool)`
  façon volumes, vs méthode `AdminList` séparée façon remote-workspaces)
  — les deux patterns coexistent déjà dans le repo, choisis celui qui
  te semble le plus lisible ici et documente pourquoi. => que propose tu ?
- Future feature de partage RW/RO par le propriétaire, avec
  traçabilité pour le dépannage ou session de travail à plusieurs — non traitée par ce fix, juste à
  garder en tête pour ne pas fermer la porte à son implémentation
  ultérieure (par ex. un futur champ `sharedWith`/`accessGrants` sur
  `model.Workspace` qui viendrait s'ajouter à la vérification
  d'ownership stricte de `fetchByID`).
