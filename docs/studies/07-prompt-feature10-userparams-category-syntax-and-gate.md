# Prompt Fable 5 — Feature 10 : `userParams` par catégorie, suppression d'`expertUserParams`, et le gate `protocolParams` manquant au connect-time

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

La Feature 7 (déjà implémentée et mergée — commits `164b23a` CRD, `b9b7cde` api, `097e9b8` frontend, `3468bc4` dev catalog) a introduit un mécanisme à deux listes sur `WorkspaceProtocol` : `UserParams []string` et `ExpertUserParams []string`, combinées par union pour former l'allow-list des noms de paramètres guacd surchargeables au connect-time — la distinction entre les deux listes n'étant que **présentationnelle** (placement `expert` vs `user` dans le formulaire, un nom présent dans les deux rendant en `expert`).

Ce prompt **change le dogme** sur trois points décidés depuis, et invalide une partie du design de la Feature 7. Ce n'est pas un ajout : c'est un remplacement.

## Ce qui existe déjà (à connaître avant de coder)

### Le registre `Category` (`operator/pkg/params/params.go:44-69`) — NE CHANGE PAS

`Category` a 7 valeurs (`display`, `audio`, `input`, `clipboard`, `session`, `security`, `connection`), assignée à chaque `Param` de la table (`params.go:109-448`), et `AllCategories()` (ligne 64) donne l'ordre canonique. `ForProtocol(protocol)` (ligne 454) filtre par protocole et trie par catégorie puis tier puis nom. **Ce mécanisme reste tel quel** — c'est la brique que ce prompt réutilise pour la nouvelle syntaxe de `userParams`, pas quelque chose à refaire.

### `UserParams`/`ExpertUserParams` — À SUPPRIMER ENTIÈREMENT

Champ CRD (`operator/api/v1alpha1/workspacetemplate_types.go:235-249`) :
```go
UserParams []string `json:"userParams,omitempty"`
ExpertUserParams []string `json:"expertUserParams,omitempty"`
```
`ExpertUserParams` n'a plus de raison d'être : la Feature 7 le justifiait par un besoin de placement (expert vs user dans le formulaire), mais ce placement est déjà et uniquement piloté par `Tier` (`params.go:23-42`, `ui`/`advanced`/`platform`) — `ExpertUserParams` faisait doublon avec une information que le registre porte déjà. Supprime le champ et **toute référence** à `ExpertUserParams`/`expertUserParams` dans le repo — la recherche exhaustive à date (`grep -rn "ExpertUserParams\|expertUserParams"`) touche :

- **CRD/opérateur** : `operator/api/v1alpha1/workspacetemplate_types.go` (champ + commentaire), `operator/api/v1alpha1/zz_generated.deepcopy.go` (régénéré par `controller-gen`, ne touche pas à la main — relance `make manifests`/le target deepcopy après avoir modifié le type), `operator/config/crd/bases/waas.xorhub.io_workspacetemplates.yaml` et `helm/waas/crds/waas.xorhub.io_workspacetemplates.yaml` (régénérés, idem), `operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go:69-70` (validation admission), `operator/internal/webhook/v1alpha1/workspacetemplate_webhook_test.go` (plusieurs cas de test nommément sur `expertUserParams`, lignes ~63-111).
- **api-server** : `api-server/internal/service/template_service.go:85,256-257,270,393` (DTO + validation + copie CR), `api-server/internal/service/workspace_service.go:549,553,931-932` (résolution de l'allow-list connect-time et exposition dans le modèle servi), `api-server/internal/service/connect_params_test.go:36,106` (tests dédiés au chevauchement).
- **frontend** : `frontend/src/types.gen.ts:312-323` (type généré — régénère-le depuis l'OpenAPI plutôt que de l'éditer à la main si le repo a un générateur, sinon édite-le en cohérence avec le DTO api-server), `frontend/src/lib/target.ts:24,68`, `frontend/src/components/ParamField.tsx:276,302` et `ParamField.test.ts:62`, `frontend/src/components/ProtocolTabs.tsx:176,197-201`, `frontend/src/components/ProtocolParamsForm.test.tsx:116`, `frontend/src/dialogs/ConnectionSettingsDialog.tsx:189,191`, `frontend/src/dialogs/CreateWorkspaceDialog.tsx:427,442,444`, `frontend/src/components/SessionOverlay.tsx:109,111`, `frontend/src/pages/admin/TemplatesPage.tsx:175,490-504` (le tri-state `locked/user/expert` de l'éditeur de template, cf. §B).
- **dev** : `hack/dev/templates-dev.yaml:60-65,71,114-115,119,158` — plusieurs templates ont un `expertUserParams` et un commentaire (ligne 61-64) documentant le chevauchement volontaire `color-depth` en double liste comme "test manuel Feature 7". Ce test manuel n'a plus de sens une fois `ExpertUserParams` supprimé — retire-le et adapte les templates à la nouvelle syntaxe (§A).
- `UnionNames` (`params.go:509-527`) est **supprimée**, pas repurposée : elle n'a jamais fait qu'une chose — fusionner deux listes (`UserParams`/`ExpertUserParams`) — et cette opération disparaît avec `ExpertUserParams` (il n'y a plus qu'UNE liste à résoudre, pas deux à fusionner). §A introduit une fonction neuve dédiée à un problème différent (expansion `cat:X` → noms), ne la fais pas hériter du nom ni du corps de `UnionNames`. Vérifie qu'aucun appelant de `UnionNames` ne traîne après le nettoyage (à date : `workspace_service.go:553` et le port TS `unionNames` dans `ConnectionSettingsDialog.tsx`/`CreateWorkspaceDialog.tsx`/`SessionOverlay.tsx`).

### Le gate `protocolParams` — TROU DE VALIDATION CONFIRMÉ, À COMBLER

Au connect-time, `workspace_service.go:539-568` :
```go
isAdmin := actor.Role == string(auth.RoleAdmin)
allowList := params.UnionNames(entry.UserParams, entry.ExpertUserParams)
if violation := params.ValidateUserOverrides(protocol, in.Params, allowList, isAdmin); violation != nil {
    return nil, apierror.Forbidden(violation.Error())
}
if len(in.Params) > 0 && !isAdmin {
    if pol := s.actorPolicy(ctx, actor); pol != nil && pol.Spec.Overrides != nil &&
        !slices.Contains(pol.Spec.Overrides.AllowedFields, waasv1alpha1.FieldProtocolParams) {
        return nil, apierror.Forbidden(...)
    }
}
```
Ce code ne vérifie QUE le droit côté **`WorkspacePolicy`** (`pol.Spec.Overrides.AllowedFields`). Il ne vérifie JAMAIS `tpl.Spec.Overrides.AllowedFields` (le `TemplateOverrides` du template lui-même, `workspacetemplate_types.go:270-275`, exposé par `tpl.Spec.FieldOverridable(field)` — méthode déjà écrite, `workspacetemplate_types.go:485-497`, `if s.Overrides == nil { return false }`).

Compare avec `policy.CheckOverrides` (`operator/pkg/policy/policy.go:552-574`), qui est la référence pour les overrides à la CRÉATION du workspace : il exige le champ dans **les deux listes** (`checkField`, lignes 564-574) — celle du template ET celle de la policy — sauf admin (bypass total) ou owner du template (bypass seulement côté template, reste soumis à la policy). Le connect-time n'applique aujourd'hui que la moitié de cette règle, et n'a pas non plus de notion d'owner. §B comble les deux.

**Cas concret qui reproduit le bug aujourd'hui** : `hack/dev/templates-dev.yaml:151-158`, le template `ubuntu-firefox` — `userParams: [enable-audio, color-depth]` mais **aucun bloc `overrides` du tout** (donc `FieldOverridable` renvoie `false` pour tout champ, y compris `protocolParams` — ce gabarit "session jetable" est délibérément verrouillé). Avec le code actuel, un utilisateur non-admin qui se connecte à un workspace basé sur ce template et envoie `color-depth` en param de connexion voit sa requête acceptée : `ValidateUserOverrides` la valide (le nom est dans `userParams`) et il n'y a pas de policy assignée à l'actor pour la bloquer (ou si la policy autorise `protocolParams`, rien côté template ne s'y oppose). C'est le trou : le template n'a **jamais explicitement autorisé** de tweak connect-time, et pourtant `userParams` produit un effet.

## Ce qu'il faut livrer

### A. `userParams` : entrées par nom OU par catégorie

Chaque élément de la liste `UserParams` (le champ `ExpertUserParams` disparaît, il n'y a plus qu'UNE liste) peut être :
- un **nom de paramètre exact** (comportement actuel, inchangé — rétrocompatible avec tous les templates existants qui n'utilisent que des noms bruts) ;
- un **sélecteur de catégorie**, préfixé `cat:`, ex. `cat:audio` — autorise TOUS les paramètres de cette catégorie pour ce protocole, résolus dynamiquement contre le registre `params.go` à la validation (webhook ET api-server), donc y compris les paramètres ajoutés à cette catégorie plus tard sans toucher au CRD ni au template. C'est explicitement le but recherché : ne pas dépendre d'un changement de CRD ou de template quand un nouveau paramètre apparaît dans une catégorie déjà déléguée.

**Décidé — pas de troisième forme combinée** genre `cat:audio;params:enable-audio,audio-servername` dans une seule chaîne : elle n'apporte rien par rapport aux deux formes ci-dessus. `["cat:audio"]` seul délègue déjà tout `enable-audio` + `audio-servername` (et tout futur paramètre `audio`) ; pour délimiter un sous-ensemble précis sans suivre les évolutions futures de la catégorie, deux entrées brutes (`["enable-audio", "audio-servername"]`) le font déjà sans nouvelle syntaxe. Un mini-langage avec séparateur interne (`;` puis `,`/`/`) aurait complexifié le parsing pour un cas d'usage qui n'existe pas.

**Décidé — sémantique en cas de coexistence `cat:X` + nom individuel de cette même catégorie dans la même liste : pas de conflit, pas de priorité.** Chaque entrée est purement additive (elle autorise, jamais elle ne retire) : l'union `cat:audio` + `audio-servername` est strictement identique à `cat:audio` seul, la présence du nom individuel est redondante mais pas contradictoire — aucune règle de priorité "unitaire > catégorie" n'est nécessaire ni sensée ici, ça ne surgirait que si on introduisait une syntaxe d'exclusion (hors scope, cf. ci-dessus). Traite ce cas dans la résolution comme un simple doublon dédupliqué (`ResolveUserParamNames`, cf. plus bas, ne doit ni erreur ni comportement spécial dessus).

Câble la résolution catégorie → noms partout où `UserParams` est aujourd'hui consommé comme une liste de noms plate :
- **Webhook** (`workspacetemplate_webhook.go:66`, `ValidateUserParamNames`) : une entrée `cat:X` doit être reconnue comme valide si `X` est une `Category` connue (`params.go` `AllCategories()`), et n'a pas besoin d'exister littéralement comme nom de param — ne la fais pas échouer en la traitant comme un nom de param inconnu.
- **api-server** (`template_service.go:253`, même fonction) : idem.
- **api-server connect-time** (`workspace_service.go:553-554`, `ValidateUserOverrides`) : l'allow-list effective passée à la fonction doit être l'expansion complète — chaque `cat:X` remplacé par tous les noms de `params.ForProtocol(protocol)` dont `Category == X` (hors `TierPlatform`, qui reste banni comme aujourd'hui), fusionnée (dédupliquée) avec les noms bruts. **Décidé — nouvelle fonction dédiée** dans `params.go`, ex. `ResolveUserParamNames(protocol string, entries []string) []string` (nom au choix, mais neuve — ne réutilise pas `UnionNames`, supprimée, cf. plus haut) plutôt que de dupliquer cette expansion dans chaque appelant.

**Décidé — où vit la résolution `cat:X` → noms (architecture serveur = source unique) :**
- **Éditeur de template** (`template_service.go` DTOs → `TemplatesPage.tsx`) : expose et consomme la liste **brute** (`cat:X` intact) — c'est la configuration elle-même qu'on édite, et l'UI (cf. ci-dessous) a besoin de savoir si une catégorie est déléguée EN BLOC (`cat:X` littéralement présent) ou seulement en partie.
- **Exposition connect-time** (`workspace_service.go:929-932`, le modèle servi à `ConnectionSettingsDialog.tsx`/`CreateWorkspaceDialog.tsx`/`SessionOverlay.tsx`) : expose la liste **déjà résolue** par `ResolveUserParamNames` — ces formulaires n'ont jamais eu besoin de connaître la notion de catégorie pour construire leur allow-list, seulement une liste de noms plate (comme aujourd'hui avec `unionNames`). Le frontend ne réimplémente donc PAS l'expansion `cat:` : il reçoit du concret, pas de syntaxe à parser, ce qui reste cohérent avec l'architecture "registre = source unique" déjà actée en Feature 7.

**Éditeur de template (`TemplatesPage.tsx:489-528`)** : le tri-state actuel `locked/user/expert` (Feature 7) devient un **binaire `locked/user`** par paramètre (plus d'`expert`), plus une action de bascule par section/catégorie ("autoriser toute la catégorie Audio" → ajoute `cat:audio` à `userParams`, retire les noms individuels de cette catégorie qui y seraient déjà pour éviter la redondance, cf. dédup ci-dessus). **Décidé — rendu** : si `cat:X` est littéralement présent dans la liste brute, la catégorie s'affiche comme "pleine" (déléguée en bloc, ex. un en-tête de section marqué "autorisée") ; sinon, la section reste visible avec chaque paramètre individuellement togglable, et tout paramètre non listé nommément apparaît grisé/désactivé plutôt que masqué (l'admin voit ce qu'il pourrait déléguer, pas seulement ce qui l'est déjà). Pas besoin de détecter par comptage le cas bord "tous les noms d'une catégorie cochés un par un sans `cat:X`" : ça reste visuellement "chaque case cochée", pas "catégorie pleine" — pas un problème à résoudre, juste un rendu légèrement différent d'un cas fonctionnellement équivalent.

### B. Le gate template `protocolParams` au connect-time (avec bypass owner)

Dans `workspace_service.go`, avant ou avec la vérification policy existante (lignes 561-567), ajoute la vérification symétrique côté template, **avec le bypass owner mirroré depuis `policy.CheckOverrides`** :
```go
if len(in.Params) > 0 && !isAdmin {
    isOwner := tpl.Spec.Overrides != nil && tpl.Spec.Overrides.Owner != "" && tpl.Spec.Overrides.Owner == actorUsername
    if !isOwner && !tpl.Spec.FieldOverridable(waasv1alpha1.FieldProtocolParams) {
        return nil, apierror.Forbidden(fmt.Sprintf(
            "template %q does not allow overriding %q (allowed: %v)", tpl.Name, waasv1alpha1.FieldProtocolParams, tpl.Spec.Overrides))
    }
    // ... vérification policy existante, toujours appliquée même à l'owner ...
}
```
Sans le droit template (ou le statut owner), `userParams` (quelle que soit sa syntaxe, §A) ne doit produire AUCUN effet — même si un nom ou une catégorie y est listée, l'utilisateur non-admin/non-owner ne peut tweaker aucun param au connect-time tant que `overrides.allowedFields` du template ne contient pas `protocolParams`. Vérifie avec le test `ubuntu-firefox` (§ ci-dessus, aucun bloc `overrides`) que ce cas est désormais rejeté pour un utilisateur ordinaire.

**Décidé — bypass owner : OUI, mirror `policy.CheckOverrides`.** Le commentaire du champ (`workspacetemplate_types.go:277-279`) est explicite : *"Owner is the platform username owning this template: that user may override any field on workspaces stamped from it, **like an admin**"* — ce n'est pas un concept scopé à la création du workspace, c'est un droit "admin pour ce template" qui doit donc s'appliquer symétriquement au connect-time. L'owner bypasse le gate **template** mais reste soumis au gate **policy** (identique à `CheckOverrides`, ligne 570-572 — `policyAllows` s'applique toujours). Récupère `actorUsername` avec le pattern déjà en place dans ce même fichier (`workspace_service.go:616-624`/`634-642` : `s.users.FindByID(ctx, actor.ID)` → `.Username`), ne réinvente pas un autre chemin pour obtenir l'identité de l'acteur.

## Contraintes à respecter

- Zéro régression sur les templates qui n'utilisent que des noms bruts dans `userParams` — c'est le chemin par défaut, pas un cas legacy à part.
- Supprime `ExpertUserParams` complètement (CRD, DTOs, validation, frontend, tests, dev catalog) — pas de champ mort, pas de compat shim, pas de `// removed` en commentaire.
- Régénère le CRD (`operator/config/crd/bases/...yaml`, `helm/waas/crds/...yaml`, `zz_generated.deepcopy.go`) via le target `manifests` du Makefile de `operator/`, ne les édite pas à la main.
- Régénère `docs/guacd-parameters.md` (`make docs-params`) si sa génération référence `expertUserParams` ou la nouvelle syntaxe `cat:`.
- Test Go dédié pour : (1) la résolution `cat:X` → noms (cas nominal, catégorie inconnue, catégorie ne contenant que du `TierPlatform` pour un protocole donné → liste vide plutôt qu'erreur, à toi de décider et documenter, coexistence `cat:X` + nom individuel de la même catégorie → résultat identique à `cat:X` seul), (2) le rejet connect-time quand `overrides.allowedFields` du template n'a pas `protocolParams` pour un acteur non-owner/non-admin (reproduis le cas `ubuntu-firefox`), (3) l'acceptation connect-time pour l'owner du template malgré l'absence de `protocolParams` dans les `allowedFields` du template, mais le rejet si la policy de cet owner ne l'autorise pas non plus, (4) mets à jour `connect_params_test.go` pour retirer les cas `ExpertUserParams` et les remplacer par des cas `cat:`.
- Test webhook dédié : une entrée `cat:X` valide n'est pas rejetée comme "nom de param inconnu" ; une catégorie qui n'existe pas (`cat:bogus`) EST rejetée avec un message clair.
- Test vitest : l'éditeur de template (§A) consomme la liste brute avec rendu "catégorie pleine" vs "grisé partiel", le formulaire de connexion consomme la liste déjà résolue exposée par l'api-server (pas d'expansion `cat:` côté frontend) — sans régression sur les tests `ParamField.test.ts`/`ProtocolParamsForm.test.tsx` existants (adapte ceux qui testaient explicitement le chevauchement `userParams`/`expertUserParams`).
- Mets à jour `hack/dev/templates-dev.yaml` : remplace les `expertUserParams` existants par des entrées `cat:` équivalentes ou par des noms bruts ajoutés à `userParams` selon ce qui illustre le mieux la nouvelle syntaxe (au moins un exemple `cat:audio` sur `ubuntu-firefox` ou `ubuntu-xfce`, en cohérence avec les catégories confirmées par la Feature 7), et vérifie qu'au moins UN template garde `userParams` renseigné SANS `protocolParams` dans `overrides.allowedFields` pour que le gate du §B reste testable manuellement (c'est déjà le cas de `ubuntu-firefox` — ne l'ajoute pas par erreur en corrigeant autre chose).

## Points ouverts (ton arbitrage)

Les arbitrages de fond sont tranchés ci-dessus (syntaxe, priorité, owner bypass, rendu éditeur, endroit de résolution). Il ne reste que des choix de nommage/implémentation sans enjeu de comportement :
- Nom exact de la fonction de résolution catégorie→noms dans `params.go` (`ResolveUserParamNames` proposé, libre à toi d'en choisir un autre du moment qu'il ne réutilise pas `UnionNames`).
- Nom du champ local portant `actorUsername` dans `workspace_service.go` et endroit exact où l'insérer par rapport au bloc policy existant (avant, ou fusionné dans la même fonction `checkField`-like) — reste cohérent avec le style déjà en place dans ce fichier.
