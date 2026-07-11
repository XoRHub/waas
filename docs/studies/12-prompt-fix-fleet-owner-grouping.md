# Prompt Fable 5 — Fix : fleet admin, regrouper les workspaces par utilisateur (sauf ceux de l'admin)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo

`FleetPage.tsx` → `WorkspacesFleet()` (`frontend/src/pages/admin/FleetPage.tsx:108-169`)
affiche **tous** les workspaces (tous propriétaires confondus, pour un
admin) dans une table plate, dans l'ordre renvoyé par l'API — pas de
tri ni de regroupement. La colonne « owner » (L129, L144) affiche
`ws.ownerId` brut (`font-mono text-xs`), pas de nom lisible.

- **Backend** : `WorkspaceHandler.List` →
  `WorkspaceService.List(ctx, actor)` (`workspace_service.go:127-150`) —
  si `actor.Role != admin`, filtre par label `ownerLabel: actor.ID`
  (L129-131) ; sinon aucune restriction, l'admin reçoit tout le
  namespace. C'est cette liste complète, non groupée, que reçoit
  `useWorkspaces()` (`hooks/useApi.ts:34-37`, `GET /api/v1/workspaces`).
- **Modèle** : `model.Workspace` (`model.go:172-...`) n'a que
  `OwnerID string` (json `ownerId`) — **pas** de nom d'utilisateur
  résolu, contrairement à `model.RemoteWorkspaceAdmin.OwnerUsername`
  (`model.go:253`, json `ownerUsername,omitempty`) qui existe déjà pour
  l'onglet « remote » de la même page.
- **Pattern à répliquer** : `RemoteWorkspaceService.AdminList`
  (`remote_workspace_service.go:459-490`) résout déjà exactement ce
  besoin pour les remote workspaces — une map `usernames map[string]string`
  peuplée à la volée via `s.users.FindByID(ctx, rw.OwnerID)` (évite les
  lookups redondants quand plusieurs rows partagent le même owner),
  consommée ensuite par `RemoteFleet()` côté frontend avec le fallback
  `rw.ownerUsername || rw.ownerId` (`FleetPage.tsx:206`).
  `WorkspaceService` a déjà accès à `s.users` (déjà utilisé dans
  `Create`, `workspace_service.go:158`) — le même pattern s'applique
  tel quel.
- **Identifier les workspaces de l'admin lui-même** : comparer
  `ws.ownerId === user.id`, `user` venant de `useAuthStore((s) => s.user)`
  côté frontend (déjà ce pattern dans `ConnectionSettingsDialog.tsx:31,52`).

## Ce qu'il faut livrer

1. **Backend** : ajoute `OwnerUsername string \`json:"ownerUsername,omitempty"\``à`model.Workspace`, et résous-le dans`WorkspaceService.List`uniquement pour la branche admin (inutile de payer un lookup
supplémentaire quand un utilisateur non-admin ne voit que ses
propres workspaces — il connaît déjà son propre nom). Reprends la
map de cache par requête, comme`AdminList` — ne fais pas un lookup
   par ligne sans cache.
2. **Génère les types** : `make generate-types` (régénère
   `frontend/src/types.gen.ts`, drift-checké en CI) plutôt que d'éditer
   `types.gen.ts` à la main.
3. **Frontend, `WorkspacesFleet()` (`FleetPage.tsx:108-169`)** :
   - Récupère l'utilisateur courant (`useAuthStore((s) => s.user)`).
   - Partitionne `workspaces.data.data` en deux : les workspaces dont
     `ownerId === user?.id` (l'admin lui-même) et le reste.
   - **Décidé — pas de dossier pour les workspaces de l'admin** : ses
     propres workspaces restent dans une section à plat, non groupée
     (« à ranger par l'admin lui-même » = pas de repli/organisation
     imposée dessus, exactement comme un utilisateur normal voit les
     siens sans dossiers).
   - **Décidé — les workspaces des autres utilisateurs sont regroupés
     par propriétaire** : une section/disclosure par `ownerId`,
     l'en-tête affichant `ownerUsername || ownerId` (même fallback que
     `RemoteFleet`), groupes triés alphabétiquement sur ce même libellé.
     Garde les colonnes de table actuelles telles quelles à l'intérieur
     de chaque groupe (pas de refonte des colonnes) ; la colonne
     « owner » devient redondante à l'intérieur d'un groupe mais ne la
     supprime pas sans nécessité — documente ton choix si tu la retires
     (ex. pour éviter la répétition visuelle du même nom sur chaque
     ligne d'un groupe).
   - **Deux vues séparées vs une seule page à deux sections** : la
     demande autorise les deux ; **par défaut**, préfère une seule page
     avec deux sections empilées (« Mes workspaces » puis « Par
     utilisateur », chaque groupe utilisateur repliable) plutôt que deux
     onglets distincts — ça évite un aller-retour de navigation pour un
     contenu qui reste la même ressource (le même `useWorkspaces()`).
     Si en l'implémentant tu juges qu'un onglet séparé est nettement
     plus lisible (ex. volumétrie importante), documente pourquoi dans
     le commit plutôt que de trancher silencieusement.
4. Garde le comportement de suppression (`remove.mutate`, L152-163)
   identique quel que soit le groupe/section où la ligne apparaît.

## Contraintes

- Ne touche pas à `RemoteFleet()` ni `VolumesFleet()` — cette partie ne
  concerne que l'onglet « workspaces » de la fleet.
- Ne touche pas au filtrage serveur pour les non-admins
  (`workspace_service.go:129-131`) — l'admin continue de recevoir la
  liste complète non filtrée, seul l'enrichissement `OwnerUsername`
  s'ajoute.
- i18n : nouvelles clés sous `admin.fleetPage.*` (en/fr) pour les
  libellés de section (« Mes workspaces » / « Par utilisateur » ou
  équivalent retenu).

## Tests

- Go : `WorkspaceService.List` en tant qu'admin renvoie
  `OwnerUsername` peuplé pour un owner existant, vide (`omitempty`) si
  l'utilisateur a été supprimé entretemps (lookup en erreur — ne fais
  pas échouer tout le `List` pour un owner introuvable, reproduis le
  comportement best-effort de `AdminList`) ; en tant qu'utilisateur
  non-admin, le champ n'a pas besoin d'être peuplé (documente si tu le
  laisses vide plutôt que de payer le lookup).
- Vitest, nouveau `FleetPage.test.tsx` (n'existe pas encore) : les
  workspaces de l'admin connecté apparaissent dans la section à plat ;
  les workspaces d'un autre owner apparaissent groupés sous son nom (ou
  son id si `ownerUsername` absent) ; suppression fonctionne depuis les
  deux sections.
- `go build ./...` + tests Go sur `api-server` ; `tsc -b` + vitest sur
  `frontend`.

## Points ouverts (ton arbitrage)

- Deux sections sur une page vs deux onglets (arbitrage donné
  ci-dessus, tranchable si tu trouves un signal fort en l'implémentant).
  => un tab serait le mieux pour bine dissocier les workspace admin du reste du cluster, oragniasé par nom utilisater et non UUID uitlsateur
- Conserver ou non la colonne « owner » à l'intérieur des groupes. que propose tu ?
