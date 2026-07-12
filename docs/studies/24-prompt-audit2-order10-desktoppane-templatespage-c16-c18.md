# Prompt Fable 5 — Audit 2 : ordre 10 (C15, C17) + constats C16, C18

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Source

`docs/studies/20-report-audit2-organisation-doublons-securite.md` est le
rapport d'audit complet (2026-07-11). Lis-le d'abord en entier,
notamment le tableau des constats §4 (lignes du C15 au C20) et le plan
d'action §5 — ce prompt t'évite de refaire la recherche fichier:ligne
pour chaque constat, mais le §4 donne le contexte et le raisonnement
complets derrière chacun.

Les numéros de ligne cités ci-dessous datent de la rédaction de ce
prompt (2026-07-12) — vérifie-les avant d'éditer, ils ont pu bouger de
quelques lignes depuis (`23-prompt-audit2-remediation-small-batch.md` a
été implémenté juste avant celui-ci et a pu toucher des fichiers
adjacents).

## Périmètre de ce prompt

Le plan d'action (§5) place **C15 et C17 dans l'ordre 10** (« les deux
vrais chantiers de test/refactor front, dans l'ordre du risque vécu »,
effort M chacun) et range **C16 et C18** dans la dernière ligne du
plan, « au fil de l'eau », avec verdict « Discutable » — pas de
chantier dédié dans l'esprit initial du rapport. Ce prompt les regroupe
quand même en un seul chantier explicite, mais **respecte la nuance que
le rapport donne à C16 et C18** plutôt que de forcer un traitement
uniforme :

- **C15** (`DesktopPane.tsx` en hooks testés) et **C17**
  (`TemplatesPage.tsx` découpée) sont traités en entier, dans cet ordre
  (le rapport le précise : « dans l'ordre du risque vécu »).
- **C16** : seule `GovernancePage.tsx` est traitée ici — c'est
  exactement ce que dit le rapport (« GovernancePage oui … les 3
  autres après C15, au fil des retouches »). `SplitViewPage.tsx`,
  `ProfilePage.tsx`, `UsersPage.tsx` restent hors périmètre de ce
  prompt, volontairement — ne les touche pas.
- **C18** : traité comme une extraction mécanique de responsabilité
  (pas une refonte), suivant le pattern déjà établi dans le repo
  (`workload.go`/`placement.go` côté opérateur,
  `workspace_events.go`/`workspace_resize.go` côté api-server) — voir
  section dédiée pour le périmètre exact.
- **Hors périmètre** : C25 (a11y), C26 (doc guacamole-common-js), C27
  (OIDC contre un IdP réel) — ce sont d'autres lignes « au fil de
  l'eau » du plan, non demandées ici, ne les traite pas.

Un commit par section (C15, C17, C16, C18), jamais un commit
fourre-tout — ce sont des changements de nature différente (refactor
frontend testable, découpage de page, tests ajoutés, extraction
backend).

---

## C15 — `DesktopPane.tsx` : extraire la logique en hooks testables

`frontend/src/components/DesktopPane.tsx` fait 376 lignes, dont un
**unique `useEffect` de ~240 lignes** (lignes 100-339) qui mélange :
connexion Guacamole/tunnel WebSocket, branche KasmVNC iframe, clipboard
desktop→browser (`client.onclipboard`, lignes 206-228), clipboard
browser→desktop (paste/focus listeners, lignes 230-271), souris
(276-299), clavier (301-305), et resize (`ResizeObserver` +
`createSessionResizer`, lignes 307-315). C'est le fichier où les 3
derniers fixes clipboard ont été vérifiés à la main faute de filet
(mémoire du repo : fixes 13/14).

Le pattern à suivre existe déjà partiellement dans le repo, mais
seulement pour la logique *pure* :

- `frontend/src/lib/clipboard.ts` (56 lignes, testé par
  `clipboard.test.ts`) exporte `ClipboardSync` — dédoublonnage
  echo-guard entre ce que le client vient d'envoyer/recevoir. Le
  câblage effectif (event listeners DOM, lecture/écriture
  `navigator.clipboard`, branchement sur le `client` Guacamole) reste
  inline dans le `useEffect`, pas testable isolément.
- `frontend/src/lib/sessionResize.ts` (52 lignes, testé par
  `sessionResize.test.ts`) exporte `createSessionResizer(...)` —
  debounce + POST du resize. Le branchement React (`ResizeObserver`,
  cycle de vie) reste inline (ligne 307-315).

`frontend/src/hooks/` existe déjà (`useApi.ts`, `useEscape.ts`,
`useEvents.ts`, `useProtocolSwitch.ts`) mais ne contient aucun hook lié
au desktop pane.

### Ce qui est attendu

Extrais deux hooks custom, exactement ceux nommés par le rapport :

1. **`frontend/src/hooks/useClipboardBridge.ts`** — encapsule la
   logique des lignes 206-271 (les deux directions clipboard) en
   s'appuyant sur `ClipboardSync` déjà existant plutôt qu'en le
   redupliquant. Signature à toi de définir, mais elle doit a minima
   prendre en entrée le client Guacamole (ou une interface minimale
   couvrant `onclipboard`/`createClipboardStream` — n'expose pas tout
   `Guacamole.Client`, juste ce dont le clipboard a besoin, pour rendre
   le hook testable sans mocker l'objet entier) et retourner ce dont
   `DesktopPane` a besoin pour continuer à fonctionner (ex. une
   fonction `setClipboard`/`sendClipboard` si `useImperativeHandle`
   (lignes 79-98) en dépend encore après extraction — vérifie ce que
   `useImperativeHandle` expose aujourd'hui et préserve ce contrat
   externe, ne casse pas l'API du `ref`).
2. **`frontend/src/hooks/useSessionResize.ts`** — encapsule le
   branchement lignes 307-315 (`ResizeObserver` + cycle de vie de
   `createSessionResizer`) en réutilisant `createSessionResizer` de
   `lib/sessionResize.ts` sans dupliquer sa logique de debounce.

Pour chacun : écris un fichier de test (`*.test.ts` ou `.test.tsx`
selon que le hook a besoin de DOM) avec `@testing-library/react`
`renderHook` (vérifie que la dépendance est déjà utilisée ailleurs dans
le repo — sinon regarde comment les hooks existants dans
`frontend/src/hooks/` sont déjà testés, s'ils le sont, pour rester
cohérent avec le pattern local). Teste au minimum : montage/démontage
propre (cleanup appelé), et pour le clipboard le comportement
echo-guard déjà couvert par `ClipboardSync` mais désormais exercé via
le hook plutôt qu'en le retestant en double.

**Ce que tu ne dois pas faire** : n'essaie pas d'extraire aussi la
connexion (tunnel, `Guacamole.Client`, souris, clavier) dans un hook —
le rapport ne nomme que `useClipboardBridge` et `useSessionResize`, et
la connexion elle-même est plus risquée à isoler (état partagé avec
tout le reste de l'effet). Garde-la inline dans `DesktopPane.tsx` pour
ce prompt.

### Vérification

- `cd frontend && npm run typecheck && npm test`.
- Comportement observable inchangé : `DesktopPane` doit continuer à
  exposer exactement la même API via `ref` (`disconnect`, `reconnect`,
  `setClipboard`, `sendClipboard`, `readRemoteClipboard`,
  lignes 79-98) — si tu changes cette surface, documente pourquoi dans
  le commit.
- Si tu as un environnement de dev k3d disponible (`hack/dev/`), un
  test manuel rapide clipboard + resize sur une session VNC/RDP réelle
  est recommandé (le canvas Guacamole ne se vérifie pas bien en
  screenshot headless — mémoire du repo sur les tentatives
  précédentes) ; sinon les tests unitaires + `npm run typecheck` sont
  suffisants pour ce prompt.

---

## C17 — `TemplatesPage.tsx` : découpage par section, sur le modèle PortalPage

`frontend/src/pages/admin/TemplatesPage.tsx` fait **896 lignes**. La
liste (`TemplatesPage`, lignes 65-165) est raisonnable ; le gros du
poids est `TemplateDialog` (lignes 205-795, **591 lignes**), un
formulaire multi-`fieldset` : identité (314-380), description
(382-392), ressources (394-424), protocoles (426-609, la section la
plus dense, avec `ProtocolTabs`/`ProtocolParamsForm`), kasmvncConfig
conditionnel (611-641), env vars (643-666, avec le sous-composant local
`EnvRow` en fin de fichier, lignes 799-896), overrides utilisateur
(668-707), placement (709-765), schedule (767-773), workload avancé en
YAML (775-792).

Le précédent à répliquer est `PortalPage` (juillet) : la page a été
réduite de 1617 à **100 lignes**
(`frontend/src/pages/PortalPage.tsx`), le reste extrait dans
`frontend/src/sections/` (`QuotaBanner.tsx` 62 l.,
`RemoteWorkspacesSection.tsx` 131 l., `VolumesSection.tsx` 75 l.,
`WorkspacesSection.tsx` 182 l.) — PortalPage importe ces sections et
leur passe ses props/callbacks, en gardant l'état "onglet actif"/"quel
dialogue ouvert" au niveau page.

### Ce qui est attendu

Découpe `TemplateDialog` en sous-composants, un par `fieldset` (ou
regroupement logique si deux `fieldset` sont trop couplés pour être
séparés proprement — à ton jugement, documente si tu regroupes).
Candidats évidents vu la structure ci-dessus : identité+description,
ressources, protocoles (probablement la plus grosse, garde-la seule),
kasmvncConfig, env vars (avec `EnvRow`), overrides, placement, schedule,
workload avancé.

Emplacement : contrairement à `PortalPage`, `frontend/src/sections/`
semble être un dossier dédié à la composition de la page portail
(4 fichiers, tous nommés autour du domaine portail) — ne mélange pas
les sections de `TemplatesPage` dedans. Crée un dossier local, par
exemple `frontend/src/pages/admin/templates/` (à toi de choisir le nom
exact, mais reste cohérent avec la convention de nommage déjà observée
côté backend pour ce même type d'extraction — un fichier par
responsabilité, nom explicite).

Contraintes :

- L'état (`input`, `workloadText`, `activeProto`, les helpers `set`/
  `patchActive`/`addProtocol`/`removeProtocol`, lignes 222-269) reste
  la source de vérité unique dans `TemplateDialog` — les sous-composants
  reçoivent leurs valeurs et des callbacks en props, ils ne dupliquent
  pas d'état local pour les mêmes données (même logique que
  `WorkspacesSection`/`VolumesSection` recevant leurs callbacks de
  `PortalPage`).
- `onSubmit` (271-284) et la validation (`validateWorkload`,
  `validateKasmVNCConfig`, lignes 24-46) restent dans `TemplateDialog`
  ou dans un module partagé si tu préfères, mais ne les déplace pas
  dans un sous-composant qui rendrait le flux de soumission moins
  lisible.
- `TemplatesPage.test.tsx` (174 lignes) existe déjà — fais-le passer
  sans le réécrire en profondeur si possible (il teste probablement le
  comportement observable, pas la structure interne) ; ajoute des tests
  unitaires ciblés sur au moins les 2-3 sous-composants les plus
  complexes que tu extrais (protocoles, kasmvncConfig) en suivant le
  même harnais que les tests de page existants
  (`renderWithProviders`/`signIn`/`createApiMock`, voir
  `FleetPage.test.tsx` ou `TemplatesPage.test.tsx` pour le pattern).

### Vérification

- `cd frontend && npm run typecheck && npm test`.
- Comportement du formulaire strictement identique (mêmes champs,
  mêmes validations, même payload soumis) — c'est un découpage, pas une
  réécriture fonctionnelle.

---

## C16 — Tests `GovernancePage.tsx` (seule page traitée dans ce prompt)

`frontend/src/pages/admin/GovernancePage.tsx` fait 546 lignes, 0 %
de couverture, aucun fichier `*.test.tsx`. Elle compose 3 sous-sections
importées (lignes 55-57, noms exacts à vérifier dans le fichier au
moment de l'exécution) : catalogue (activer/désactiver des images),
policies (édition YAML brute), usage (consommation par utilisateur).
C'est la seule des 4 pages 0 % que le rapport priorise explicitement :
« elle édite la policy, une régression silencieuse touche la
gouvernance » — les 3 autres (`SplitViewPage`, `ProfilePage`,
`UsersPage`) sont **hors périmètre de ce prompt**, ne les touche pas.

### Ce qui est attendu

Écris `frontend/src/pages/admin/GovernancePage.test.tsx`, avec le même
harnais que les tests de page admin déjà existants
(`renderWithProviders`/`signIn`/`createApiMock`, modèle direct :
`FleetPage.test.tsx` ou `TemplatesPage.test.tsx`). Avant d'écrire les
tests, lis entièrement `GovernancePage.tsx` (et les 3 sous-composants
qu'elle importe — vérifie s'ils vivent dans le même fichier ou sont
déjà séparés) pour identifier les flux réels ; couvre au minimum :

- Rendu initial des 3 sections avec des données mockées via
  `createApiMock`.
- Catalogue : activer/désactiver une image déclenche le bon appel API
  mocké.
- Policies : édition + soumission d'un YAML valide déclenche le bon
  appel API ; soumission d'un YAML invalide affiche une erreur sans
  appeler l'API (comportement à vérifier dans le code — ne suppose pas,
  regarde comment `YamlEditor` gère déjà ce cas ailleurs, ex. dans
  `TemplateDialog`/`TemplatesPage.tsx` pour le pattern de validation
  YAML côté page).
- Usage : rendu correct des données de consommation mockées.

### Vérification

`cd frontend && npm run typecheck && npm test` ; vérifie que le
fichier ajouté fait effectivement remonter la couverture de
`GovernancePage.tsx` au-dessus de 0 % (`npm run test -- --coverage`
avec les flags déjà en place depuis C14, ou équivalent — vérifie la
commande exacte dans `package.json`/CI).

---

## C18 — Extraire les blocs lifecycle/status des fichiers fourre-tout

`operator/internal/controller/workspace_controller.go` (1109 lignes) et
`api-server/internal/service/workspace_service.go` (1188 lignes)
grossissent malgré des voisins bien découpés. Le rapport propose « pas
de chantier dédié » mais « imposer la règle + extraire au passage de
la prochaine feature (lifecycle/status pour le contrôleur) » — ce
prompt fait cette extraction maintenant, comme un **déplacement
mécanique de code, pas une refonte** : mêmes signatures, même
comportement, tests verts avant/après.

`remote_workspace_service.go` (600 lignes) est **hors périmètre** :
contrairement aux deux autres, sa structure lue est déjà cohérente
autour d'une seule responsabilité (CRUD remote workspace + Wake-on-LAN)
— pas de sous-groupe évident à en extraire, ne le touche pas dans ce
prompt.

### 18.1 — `operator/internal/controller/workspace_status.go` (nouveau)

Le repo a déjà deux fichiers satellites de `workspace_controller.go`
qui donnent le pattern exact à suivre — regarde en particulier
l'en-tête de `placement.go` (lignes 3-8) : un commentaire de package en
tête de fichier qui explique la responsabilité extraite. Réplique cette
convention.

Déplace dans `workspace_status.go` le bloc contigu de gestion
status/conditions (`workspace_controller.go` lignes 894-969 au moment
de la rédaction — vérifie les lignes exactes avant de couper/coller) :
`patchStatus`, `setUnready`, `setCondition`, `setDriftCondition`,
`hasDriftCondition`, `setTypedCondition`. Ajoute un commentaire de
package en tête expliquant le rôle (ex. : « Status: this file owns
patching WorkspaceStatus and its conditions. »), sur le modèle de
`placement.go`.

**Ne déplace pas** le calcul de phase inline dans `Reconcile` (les deux
blocs symétriques down/running, lignes ~300-415) — c'est trop couplé
au reste de `Reconcile` pour une extraction mécanique sûre ; laisse
`Reconcile` continuer à appeler les helpers désormais dans
`workspace_status.go`, ne touche pas à sa structure de contrôle.

### 18.2 — `api-server/internal/service/workspace_lifecycle.go` (nouveau)

Le repo a déjà `workspace_events.go` (116 lignes, méthode `Events`) et
`workspace_resize.go` (107 lignes, `WithPodExecutor`/`Resize`) comme
satellites de `workspace_service.go`, nommés `workspace_<feature>.go`,
méthodes sur `*WorkspaceService`. Suis cette convention.

Déplace dans `workspace_lifecycle.go` le groupe cohérent d'actions de
cycle de vie identifié par la recherche préalable à ce prompt :
`SetPaused` (lignes 388-440), `UpdateOverrides` +
`updateOverridesSummary` (441-531), `Reload` (532-556) — vérifie les
lignes exactes avant de couper/coller, elles datent de la rédaction de
ce prompt. Garde les signatures identiques (méthodes sur
`*WorkspaceService`, mêmes noms, même visibilité).

Si `workspace_resize_test.go` existe pour `workspace_resize.go` mais
qu'aucun test dédié n'existe pour `Events`/`SetPaused`/
`UpdateOverrides`/`Reload` (vérifie), ne les ajoute pas dans ce
prompt — le périmètre ici est l'extraction, pas l'ajout de couverture
(ça relèverait d'un autre constat).

### 18.3 — La règle, documentée

Le rapport demande d'« imposer pas de nouvelle responsabilité dans ces
fichiers ». Ajoute un commentaire de package en tête de
`workspace_controller.go` et de `workspace_service.go` (même esprit que
`placement.go`) indiquant que toute nouvelle responsabilité doit vivre
dans un fichier satellite dédié (`workspace_<feature>.go` côté
api-server, fichier nommé par responsabilité côté opérateur), pas être
ajoutée à ces deux fichiers. Reste bref — une ou deux phrases, pas un
essai.

### Vérification

- `cd operator && go build ./... && go test ./...`.
- `cd api-server && go build ./... && go test ./...`.
- Diff attendu : essentiellement des déplacements de blocs de code
  identiques entre fichiers (+ 2 nouveaux en-têtes de fichier, + 2
  commentaires de règle) — si le diff montre des changements de
  logique au-delà du déplacement, tu es sorti du périmètre de ce
  prompt, reviens en arrière.

---

## Contraintes générales

- N'affaiblis jamais un gate existant (seuils Trivy, ratchets de
  couverture, gates de sécurité) pour faire passer la CI plus
  facilement.
- Chaque section (C15, C17, C16, C18) est indépendante des trois
  autres — traite-les dans l'ordre du document si possible (c'est
  l'ordre de risque du rapport), mais rien n'empêche un ordre différent
  si tu as une bonne raison.
- Un commit par section, jamais un commit fourre-tout.
- Build/tests verts avant de considérer une section terminée :
  `cd frontend && npm run typecheck && npm test` pour C15/C16/C17 ;
  `go build ./... && go test ./...` dans `operator/` et `api-server/`
  pour C18.
- Si un fichier ou une ligne citée ici a déjà changé au point de rendre
  une instruction obsolète, adapte-toi au code réel plutôt que de
  suivre le prompt à la lettre — documente l'écart dans le message de
  commit.
