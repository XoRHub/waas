# Prompt Fable 5 — Feature 7 : sections thématiques pour les params protocole + split userParams/expertUserParams

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

Les paramètres de connexion guacd (VNC/RDP/SSH) viennent d'un registre unique côté Go, `operator/pkg/params/params.go`. Certains sont manifestement liés entre eux sémantiquement (ex. `enable-audio`/`audio-servername`, ou les params de qualité d'image) mais sont aujourd'hui rendus en liste plate côté frontend, sans aucun regroupement thématique. Par ailleurs, le CRD expose un mécanisme `userParams` (liste de noms autorisables au connect-time) qui pourrait gagner à être scindé en deux niveaux combinables plutôt qu'une seule liste plate.

## Ce qui existe déjà (à connaître avant de coder)

**Le registre `Param`** (`params.go:51-70`) : `Name`, `Protocols []string`, `Kind`, `Enum`, `Min/Max *int`, `Default`, `Tier`, `Live bool`, `Description`.

**`Tier` a trois valeurs** (`params.go:23-38`, doc-comment) :

- `TierUI = "ui"` — rendu dans les formulaires portail.
- `TierAdvanced = "advanced"` — d'après le commentaire du code, "settable in the CR/template only... or the template editor's advanced section — never in end-user forms". **Vérifie ce point en codant** : `ParamField.tsx:270-279` (`tieredParams`) range `tier:['ui']` → `simple`, `tier:['advanced']` → `advanced`, et `ProtocolTabs.tsx:163-219` (`ProtocolParamsForm`) affiche `simple` toujours, `advanced` derrière un toggle "afficher les params avancés" (lignes 185, 207-216) — **dans le formulaire utilisateur du portail, pas seulement dans l'éditeur de template**. Il y a donc une tension entre le commentaire du registre (advanced = jamais en formulaire utilisateur) et le comportement réel du code (advanced = accessible en formulaire utilisateur derrière un toggle) — clarifie et aligne les deux avant de construire dessus, ou documente explicitement que c'est le comportement voulu.
- `TierPlatform = "platform"` — jamais settable par le template ni l'utilisateur, banni par `ValidateTemplateParams` (ligne 505), `ValidateUserParamNames` (ligne 523), `ValidateUserOverrides` (ligne 540). `TierAdvanced` est traité **identiquement à `TierUI`** dans ces trois fonctions de validation — la distinction ui/advanced n'existe aujourd'hui que dans le **rendu**, pas dans la politique de validation.

**`GET /api/v1/meta/protocols`** (`api-server/internal/handler/meta_handler.go:23-39`) sert `params.ForProtocol(proto)` (params.go:416-434, trié ui d'abord puis advanced puis platform) tel quel, `Tier` inclus.

**`userParams` côté CRD** — `WorkspaceProtocol` (`operator/api/v1alpha1/workspacetemplate_types.go`) :

- `Params map[string]string` (ligne 227, json `params,omitempty`) : valeurs verrouillées/par défaut du template.
- `UserParams []string` (ligne 232, json `userParams,omitempty`) : **une liste de NOMS de paramètres** que l'utilisateur peut surcharger au connect-time — pas des valeurs. Tout nom absent de cette liste reste verrouillé à `Params`.
- Ce filtre fin s'exerce **sous** un droit plus large : `TemplateOverrides.AllowedFields []OverridableField` (ligne 249), avec `FieldProtocolParams = "protocolParams"` (ligne 42) qui autorise ou non toute tweak connect-time de params — cf. `connectTimeRights` (`operator/pkg/policy/overrides.go:60-62`), commentaire : "connect-time guacd parameter tweaks, enforced by the api-server on /connect (template userParams stays the fine-grained filter)".

**Chaîne de consommation de `UserParams`** : `api-server/internal/service/template_service.go` (lignes 84, 251, 259, 373, DTO + `ValidateUserParamNames`) ; `workspace_service.go:552` (`params.ValidateUserOverrides(protocol, in.Params, entry.UserParams, isAdmin)` — les admins court-circuitent la liste) ; `workspace_service.go:929` (copie `entry.UserParams` dans le modèle servi). **L'opérateur n'a pas besoin de `UserParams` pour construire les params guacd** — c'est un garde-fou strictement côté api-server ; le webhook/l'opérateur ne valide que `Params` (valeurs verrouillées) via `ValidateTemplateParams`.

**Le test d'exhaustivité `pkg/policy`** (`overrides_registry_test.go:24`, `TestOverrideRegistryIsExhaustive`) couvre les champs de `WorkspaceOverrides`/`WorkspaceSpec` — **un nouveau champ `expertUserParams` sur `WorkspaceProtocol` ne le fera pas échouer** (ce n'est ni l'un ni l'autre), mais tu dois quand même ajouter une couverture de test dédiée pour la logique de fusion/priorité côté api-server, et mettre à jour la description de `FieldProtocolParams` dans `overrides.go:61` pour mentionner le nouveau split.

**Frontend — aucun regroupement thématique aujourd'hui** : `ProtocolTabs.tsx` = un onglet par protocole, `ProtocolParamsForm` = une grille plate (`simple` toujours visible, `advanced` derrière toggle), aucune section "Affichage"/"Audio"/"Presse-papier"/"Sécurité".

**Regroupements confirmés (noms exacts du registre, ne les invente pas)** :

- **Audio (VNC)** : `enable-audio` (ligne 116, ui), `audio-servername` (ligne 121, advanced).
- **Affichage/qualité (VNC/RDP)** : `color-depth` (ligne 96, ui, enum 8/16/24/32), `swap-red-blue` (ligne 101, ui), `cursor` (ligne 106, ui, enum local/remote), `force-lossless` (ligne 111, ui), `resize-method` (ligne 145, ui, RDP). **Pas de param `dpi`** dans le registre (géré côté plateforme via un commentaire de doc `workspacetemplate_types.go:225`, pas une entrée `Param` — ne l'ajoute pas à ce groupe).
- **Presse-papier** : `disable-copy`/`disable-paste` (lignes 84-92, partagés, ui, live), `clipboard-encoding` (ligne 136, VNC, advanced), `normalize-clipboard` (ligne 235, RDP, advanced).
- **Sécurité/session** : `read-only` (ligne 79, partagé, ui), `security`, `ignore-cert`, `console`, `disable-auth` (RDP, certains `TierPlatform` — vérifie lesquels sont vraiment éditables avant de les grouper avec les autres).

## Ce qu'il faut livrer

### A. Sections thématiques dans le formulaire de params

Ajoute un champ de catégorisation piloté par le registre (ex. `Category string` sur `Param`, valeurs du style `"display"`, `"audio"`, `"clipboard"`, `"security"`) — pas une logique de regroupement hardcodée côté frontend, pour rester cohérent avec l'architecture "registre = source unique" déjà en place pour `Kind`/`Tier`. Assigne une catégorie à chaque param existant (utilise les regroupements confirmés ci-dessus comme base). Fais rendre `ProtocolParamsForm` par catégorie (un sous-titre par section), en conservant la distinction `simple`/`advanced` **à l'intérieur** de chaque section plutôt que de la remplacer — décide et documente si le toggle "afficher advanced" reste global ou devient par section.

### B. `userParams` / `expertUserParams` combinables

Ajoute un second champ `ExpertUserParams []string` (json `expertUserParams,omitempty`) à côté de `UserParams` sur `WorkspaceProtocol`. Les deux listes sont **combinatoires, pas exclusives** : l'ensemble effectif de noms surchargeable au connect-time est l'union des deux. `expertUserParams` est **prioritaire** en cas de règle contradictoire sur un même nom présent dans les deux listes — précise et code cette notion de priorité (par exemple : si un nom apparaît dans `expertUserParams`, applique-lui les règles de validation "avancées"/moins restrictives même s'il apparaît aussi dans `userParams` avec des règles plus strictes ; documente exactement ce que "prioritaire" change en pratique dans ton implémentation, ce n'est pas évident tel quel puisque ce sont des listes de noms, pas des règles par nom).

Câble ce nouveau champ dans la même chaîne que `UserParams` aujourd'hui : `template_service.go` (DTO + validation des noms), `workspace_service.go:552` (`ValidateUserOverrides` doit prendre en compte l'union des deux listes), `workspace_service.go:929` (exposition dans le modèle servi).

## Contraintes à respecter

- Zéro changement de contrat pour les templates existants qui n'utilisent que `userParams` — `expertUserParams` est un champ additionnel optionnel, pas un remplacement.
- Ajoute un test d'exhaustivité pour la nouvelle catégorisation (§A), sur le modèle de `overrides_registry_test.go`/le test D1 protocole-enum déjà présent dans le repo (`docs/studies/audit-2026-07.md` §D1) — chaque `Param` doit avoir une `Category` non vide, pour empêcher un nouveau param d'être ajouté sans catégorie.
- Test Go dédié pour la logique d'union/priorité `userParams`/`expertUserParams` (§B) — ne te contente pas de réutiliser les tests existants de `ValidateUserOverrides`, ajoute des cas couvrant explicitement le chevauchement des deux listes.
- Test vitest pour le rendu par sections (`ProtocolParamsForm` groupé).
- Mets à jour `overrides.go:61` (description de `FieldProtocolParams`) et régénère `docs/guacd-parameters.md` (`make docs-params`) si `Category` y apparaît.
- Environnement de dev : mets à jour au moins un template de `hack/dev/templates-dev.yaml` pour illustrer `expertUserParams` en plus de `userParams`, afin que le split soit testable manuellement.

## Points ouverts (ton arbitrage)

- Nom exact du nouveau champ CRD (`expertUserParams` proposé par la demande initiale) — vérifie qu'il n'entre pas en collision avec un terme déjà utilisé ailleurs dans le registre d'overrides avant de le fixer définitivement.
- Sémantique précise de "prioritaire" en cas de chevauchement (§B) — plusieurs interprétations valables, tranche et documente-la clairement dans le commentaire du champ CRD et dans `overrides.go`.
- Le toggle "advanced" reste-t-il global au protocole ou devient-il par section (§A) — les deux sont défendables, la seconde option est plus cohérente avec le regroupement mais plus de travail visuel.
  ARBITRAGE: oranigsé par section cela semble plus coherent en effet
