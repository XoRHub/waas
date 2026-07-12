# Prompt Fable 5 — Feature 13 v2, volet B (waas-fable) : création d'un workspace sans WorkspaceTemplate ("orphelin")

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

**Ce document est le volet B d'une feature scindée en deux prompts**
(relecture architecte, voir aussi
`docs/studies/26-prompt-feature13-direct-image-deploy-waas-v2.md`, le
volet A — catalogue d'images). **Ce volet B dépend du volet A** : il
consomme `WorkspaceImage.status.catalog.entries` (le champ imbriqué
`CatalogImage.discovered`, exposé par `GET /catalog`) pour son picker.
Implémente le volet A d'abord si ce n'est pas déjà fait.

**Point d'architecture à trancher AVANT toute implémentation** (§
Architecture ouverte ci-dessous) : le placement namespace d'un
workspace créé par ce chemin. Ne commence pas le codage sans avoir
arbitré ce point — il touche potentiellement `operator/pkg/policy/policy.go`
et le contrat de `WorkspaceTemplate`/`WorkspaceImage` au-delà du
périmètre de cette seule feature.

## Contexte et objectif

Un utilisateur (si sa `WorkspacePolicy` l'y autorise) choisit
directement une image dans un registre approuvé
(`WorkspaceImage.spec.registry`, catalogué par le volet A), sans qu'un
admin ait dû créer de `WorkspaceTemplate` pour cette image précise.
L'admin peut faire la même chose sans autorisation de policy
particulière (nuance, voir § Bypass admin).

## Architecture ouverte : placement namespace pour un workspace sans template

**Ce point n'est pas encore tranché — à décider avant d'implémenter.**

Aujourd'hui, le placement (dans quel namespace atterrit un workspace)
suit la précédence "template pattern > pattern global opérateur >
built-in" (`policy.ResolvedDefaultNamespace`, `operator/pkg/policy/policy.go:536-541`) :

```go
func ResolvedDefaultNamespace(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, id Identity, globalPattern string) (string, error) {
	pattern := naming.EffectivePattern(tpl.Spec.PlacementNamespacePattern(), globalPattern)
	return naming.ResolveNamespace(pattern, PlacementValues(ws, tpl, id))
}
```

`PlacementValues` (`policy.go:527-534`) et cette fonction ne lisent
QUE le template (`tpl.Spec.PlacementNamespacePattern()`, `tpl.Name`,
`tpl.Spec.OS`) — aucune notion de `WorkspaceImage` n'entre dans ce
calcul nulle part aujourd'hui. `Placement *WorkspacePlacement` vit sur
`WorkspaceTemplateSpec` (`operator/api/v1alpha1/workspacetemplate_types.go:389-394`),
pas sur `WorkspaceImageSpec`.

Le template synthétique de ce volet (§ Décision d'architecture centrale
n°1) ne fixe jamais de `Spec.Placement` custom — donc pour un workspace
"orphelin", la précédence se réduit toujours à "pattern global >
built-in", en sautant silencieusement le premier palier. Deux
approches possibles, à arbitrer :

**Option 1 — accepter l'asymétrie, documenter-la seulement.** Le
workspace orphelin n'a simplement jamais de pattern de placement
custom (pas plus grave qu'un template admin-authored qui n'en définit
pas non plus). Coût : `GET /workspaces/namespace-preview` (utilisé par
`useNamespacePreview`, `frontend/src/hooks/useApi.ts:337-347`,
"précédence template > global > built-in, résolue SERVER-SIDE, jamais
côté UI") est aujourd'hui câblé sur un `templateRef` obligatoire — or
en mode orphelin, il n'existe PAS de template avant la création
(synthétisé après soumission, § api-server). Il faudrait a minima
étendre cet endpoint à accepter un appel sans template (ou avec un
paramètre `?image=X`), renvoyant directement le palier global/built-in
— sinon l'aperçu de namespace ET le rattachement d'un volume retenu
existant (`attachableVolumes`, filtré sur le namespace prévisualisé,
`CreateWorkspaceDialog.tsx:92-94`) disparaissent silencieusement pour
ce mode, une régression de transparence/fonctionnalité non documentée
si on ne la traite pas explicitement.

**Option 2 — donner un vrai point d'ancrage structurel à l'image.**
Ajoute `WorkspaceTemplateSpec.WorkspaceImageRef *corev1.LocalObjectReference`
(nom d'une `WorkspaceImage`), **mutuellement exclusif avec
`WorkspaceTemplateSpec.Image`** (même style de XValidation que
`WorkspaceImageSpec` a déjà pour `image`/`registry` — "exactly one of
image or registry must be set"). Le template synthétique de ce volet
poserait `WorkspaceImageRef` au lieu de copier la référence exacte dans
`Spec.Image`. Si `WorkspaceImageSpec` gagne à son tour un `Placement`
optionnel, la précédence s'élargit à 4 paliers (template > **image
(nouveau)** > global > built-in), et `PlacementValues`/`ResolvedDefaultNamespace`
doivent résoudre le `WorkspaceImage` référencé (nouvelle dépendance :
ces fonctions, aujourd'hui pures sur le seul template, auraient besoin
d'un client K8s ou d'un paramètre `img *WorkspaceImage` déjà chargé —
`policy.LoadOf` le fait déjà pour les ressources, `L383`, precedent à
suivre). Bénéfice : un seul chemin de résolution de placement pour
tous les workspaces (template classique OU synthétique), pas de
branche spéciale "orphelin = pas de préférence de placement possible".
Coût : touche `enforce()`, `buildPodTemplate`, et tout consommateur de
la précédence actuelle — un rayon d'impact réel, au-delà du périmètre
"créer un workspace sans template" seul.

**Recommandation de départ (à confirmer, pas figée)** : commencer par
l'Option 1 pour ce volet — elle suffit à livrer la feature sans
toucher au contrat `WorkspaceTemplate`/`WorkspaceImage` au-delà de ce
que ce prompt prévoit déjà. L'Option 2 reste une évolution ultérieure
possible (le fait de préférer `WorkspaceImageRef` à une copie brute de
`Spec.Image` dans le template synthétique n'empêche PAS de démarrer
avec l'Option 1 pour le placement — les deux questions, référence
structurelle et placement à 4 paliers, sont séparables). Si l'Option 2
est retenue malgré tout, traite-la comme une décision d'architecture à
part entière avec sa propre revue, pas un sous-point de ce prompt.

## Décision d'architecture centrale n°1 : synthétiser un WorkspaceTemplate, ne pas contourner l'enforcement existant

**Ne crée PAS de nouveau champ sur `Workspace.Spec`** (pas de
`imageRef`/`catalogImageRef`). `Workspace.Spec.TemplateRef` reste
l'unique pointeur de provisioning.

Raison : `enforce()` (`operator/internal/webhook/v1alpha1/workspace_webhook.go`,
fonction `enforce()`, `policy.Resolve` appelé L370),
`policy.LoadOf`/`policy.PlacementValues`/`policy.ResolvedDefaultNamespace`
(`operator/pkg/policy/policy.go`) et `buildPodTemplate`
(`operator/internal/controller/workload.go`) prennent TOUS un
`*WorkspaceTemplate` réel en paramètre et l'utilisent en profondeur.
Dupliquer ce chemin pour un provisioning "sans template" doublerait la
maintenance et rouvrirait le risque de divergence que
`policy.OwnerLoads` documente déjà avoir corrigé une fois ("hand-copied
loops whose vanished-template fallbacks had silently diverged").

**Solution retenue (inchangée depuis la v1)** : l'api-server
**synthétise un `WorkspaceTemplate` privé, à usage unique**, 1:1 avec
le futur workspace — `spec.image` = la référence exacte choisie (ou
`spec.workspaceImageRef` si l'Option 2 ci-dessus est retenue),
`spec.protocols`/`spec.resources` dérivés du choix utilisateur borné
par la `WorkspaceImage` — puis appelle le chemin de création de
workspace **existant et inchangé** avec `templateRef` pointant sur ce
template synthétique. Toute la chaîne d'enforcement/provisioning
(webhook, quotas, `buildPodTemplate`, placement) s'applique alors mot
pour mot.

Le template synthétique est marqué par un label (pas un nouveau champ
de schéma) :
- `waas.xorhub.io/synthetic-template: "true"`
- `waas.xorhub.io/owner: <owner UUID>` (label déjà existant,
  `operator/api/v1alpha1/identity.go:71`, réutilisé tel quel)

Ajoute la constante de label dans `operator/api/v1alpha1/identity.go`
(à côté de `LabelOwner`, `LabelRetained`) :
```go
LabelSyntheticTemplate = "waas.xorhub.io/synthetic-template"
```

### Mitigation watch/informer — à livrer avec le label, pas après

`operator/internal/controller/workspace_controller.go` a déjà un
`Watches(&waasv1alpha1.WorkspaceTemplate{}, mapTemplateToWorkspaces,
predicate.GenerationChangedPredicate{})` (fonction `SetupWithManager`)
pour la détection de drift (`docs/adr/0001`) : un admin édite un
template, les workspaces qui le référencent doivent recevoir la
condition `TemplateDrifted` sans attendre un requeue périodique.

Un template synthétique est **immuable par construction** (jamais
réédité, jamais partagé — voir § Contraintes). Le drift ne peut donc
structurellement jamais s'y produire : le laisser transiter par ce
watch coûte un événement CREATE + un événement DELETE par cycle de vie
de workspace direct, pour un bénéfice nul. Ajoute un filtre au
predicate existant (une fonction `predicate.NewPredicateFuncs` ou un
`predicate.And` avec un check sur
`obj.GetLabels()[waasv1alpha1.LabelSyntheticTemplate] != "true"`) pour
que `mapTemplateToWorkspaces` ne soit même pas invoqué sur ces objets.
Ne touche pas au comportement du watch pour les templates
admin-authored.

## Bypass admin : pas de nouveau chemin de code

Doctrine du projet : "the bypass stays a VISIBLE, auditable
WorkspacePolicy CR — never a code path" (`helm/waas/values.yaml`, bloc
`adminPolicy`). **Ne code PAS** de bypass spécial "role admin ⇒ direct
deploy autorisé" dans le webhook :
- Nouveau champ `WorkspacePolicy.spec.directDeploy` (booléen), champ de
  policy comme un autre.
- La policy bootstrap tout-droits (`values.yaml` bloc `adminPolicy`,
  `gitops/governance/policies.yaml`) doit inclure `directDeploy: true`.
- L'admin garde le bypass générique existant
  (`operator.policyBypass`) pour les accès kubectl directs, inchangé.

## Ce qu'il faut livrer

### 1. `WorkspacePolicy` — nouveau droit `directDeploy`

`operator/api/v1alpha1/workspacepolicy_types.go` — nouveau champ sur
`WorkspacePolicySpec`, juste après `RemoteWorkspaces` :

```go
// DirectDeploy opts the governed users into creating a workspace
// directly from an admin-approved registry's catalog, without an
// admin-authored WorkspaceTemplate. Which images they may pick is
// still governed by the EXISTING Images/AllowedGroups gates — this
// field only lifts the "a template must exist" requirement. Absent or
// false = feature hidden and refused (fail closed), same convention
// as RemoteWorkspaces.
// +optional
DirectDeploy bool `json:"directDeploy,omitempty"`
```

`operator/pkg/policy/policy.go` — nouvelle fonction miroir de
`RemoteWorkspacesAllowed` (L618) :

```go
// DirectDeployAllowed reports whether the resolved policy opts its
// users into direct-from-catalog workspace creation. Nil policy = denied.
func DirectDeployAllowed(pol *waasv1alpha1.WorkspacePolicy) bool {
	return pol != nil && pol.Spec.DirectDeploy
}
```

Nouveau `Reason` dans le bloc `const` (L61) :
```go
ReasonDirectDeployNotAllowed Reason = "DirectDeployNotAllowed"
```

### 2. Webhook — nouveau gate pour template synthétique

`operator/internal/webhook/v1alpha1/workspace_webhook.go`, fonction
`enforce()` : juste après `policy.Resolve` (L370, `pol` disponible),
ajoute :

```go
if tpl.Labels[waasv1alpha1.LabelSyntheticTemplate] == "true" && !policy.DirectDeployAllowed(pol) {
	return warnings, &policy.Denial{Reason: policy.ReasonDirectDeployNotAllowed,
		Message: fmt.Sprintf("policy %q does not grant direct-from-catalog deployment", pol.Name)}
}
```

Le reste de `enforce()` — `FindImage`, `ImageAllowed`,
`CheckTagDiscipline`, `CheckProtocol`, `CheckOverrides`, `CheckLimits`
— s'applique SANS modification.

### 3. api-server — synthèse du template + nouvel endpoint

Nouvelle route, `api-server/internal/server/router.go` (dans le bloc
`r.Route("/workspaces", ...)`, à côté de la route existante à L80, ou
en miroir du bloc `/workspace-templates` L133) :

```go
r.Post("/workspaces/direct", h.Workspaces.CreateDirect)
```

Nouveau type dans `api-server/internal/service/`, champs :
`catalogImage string` (référence exacte choisie, doit apparaître dans
`WorkspaceImage.status.catalog.entries[].image` d'une entrée pour
laquelle `DirectDeployAllowed` + `policy.Images` l'autorisent — le
service DOIT revérifier ces deux points côté api-server aussi, en plus
du webhook, avant même de tenter la création — cohérent avec la
validation amont déjà faite pour `templateRef` par
`WorkspaceService.Create`), `protocol string`,
`resources *corev1.ResourceRequirements`, `displayName string`.

Logique de `WorkspaceService.CreateDirect` :
1. Résout la `WorkspaceImage` propriétaire de l'entrée catalogue
   choisie (celle dont `status.catalog.entries[].image == in.CatalogImage`).
2. Construit un `*waasv1alpha1.WorkspaceTemplate` en mémoire : `Name`
   généré (ex. `direct-<uuid court>`, même discipline que
   `generateWorkspaceName` existant), `Labels: {synthetic-template: "true", owner: ownerID}`,
   `Spec.OS` = celui de l'entrée catalogue (défaut `linux`),
   `Spec.Image` = `in.CatalogImage`, `Spec.Protocols` dérivé de
   `WorkspaceImage.Spec.Protocols` ∩ `in.Protocol`, `Spec.Resources` =
   `in.Resources` ou le défaut de la `WorkspaceImage`. **`Spec.Overrides`
   reste nil** : ce template n'est jamais réutilisé ni partagé, la
   délégation d'override n'a pas de sens ici.
3. `s.kube.Create` ce template (le SA api-server a déjà `create` sur
   `workspacetemplates`, `helm/waas/templates/api-server.yaml:19` —
   aucun nouveau RBAC côté api-server).
4. Appelle exactement le même chemin que `WorkspaceService.Create`
   avec `TemplateRef` = le nom généré — factorise plutôt que dupliquer.
5. Si la création du `Workspace` échoue APRÈS que le template ait été
   créé, supprime le template synthétique avant de renvoyer l'erreur.

**Nettoyage à la suppression** : quand un `Workspace` est supprimé, si
son `templateRef` pointe vers un template portant
`labels[synthetic-template]=true`, supprime aussi ce template. Regarde
`operator/internal/controller/workspace_teardown_test.go` pour le point
d'accroche exact (reconciler operator, pas api-server — le workspace
peut aussi être supprimé par kubectl/GitOps direct). **RBAC operator** :
`workspacetemplates` n'a aujourd'hui que `[get, list, watch]`
(`helm/waas/templates/operator.yaml:67-69`) — ajoute `delete` pour ce
chemin de nettoyage.

**Pourquoi pas un `ownerReference` plutôt qu'un hook de nettoyage codé
à la main** : l'idiome K8s naturel pour ce genre de cascade serait un
`ownerReference` du `WorkspaceTemplate` synthétique vers son
`Workspace` — GC natif, gratuit, couvre tous les chemins de suppression
(api-server, kubectl, GitOps) sans code applicatif. **Ne le fais pas** :
`WorkspaceTemplate` vit dans `s.namespace` (namespace plateforme, ex.
`waas` — `workspace_service.go`, tous les `client.ObjectKey{Namespace:
s.namespace, ...}` sur `tpl`) alors que le `Workspace` est placé dans
`EffectiveTargetNamespace()`, un namespace différent selon le pattern de
placement. Les `ownerReferences` K8s ne fonctionnent pas cross-namespace
(le GC ignore silencieusement une référence vers un objet d'un autre
namespace — pas d'erreur visible, juste un objet jamais nettoyé). Le
hook explicite ci-dessus est donc la SEULE option viable, pas un choix
de style — ne le remplace pas par un `ownerReference` en relecture
future sans revérifier ce point.

**Audit orphelins** : `hack/audit-orphans.sh` ne couvre aujourd'hui pas
`WorkspaceTemplate` (grep confirmé : zéro résultat). Étends-le : un
template `synthetic-template=true` sans workspace vivant le
référençant est un orphelin. Ne touche pas au comportement pour les
templates admin-authored.

Le script est aujourd'hui **manuel, à la demande** (`hack/audit-orphans.sh
[--clean]`, aucune référence dans `.github/workflows/`, `.gitlab/ci/`
ou un `CronJob` Helm — grep confirmé, zéro résultat) pour TOUTES les
catégories d'orphelins qu'il couvre déjà (objets `managed-by
waas-operator` sans workspace vivant, namespaces vidés, volumes
retenus) — pas seulement celle qu'ajoute cette feature. Un template
synthétique orphelin (crash de l'api-server entre la création du
template et celle du workspace, § 3 pt. 5) hérite donc de la même
fenêtre de latence que tout le reste jusqu'au prochain passage manuel
du script. **Décision : ne pas introduire de `CronJob` spécifique à
cette feature** — ce serait incohérent avec la posture manuelle
existante pour toutes les autres catégories, sans qu'aucune raison
propre aux templates synthétiques ne justifie de les traiter à part
(un template orphelin n'expose rien de plus qu'un `WorkspaceTemplate`
admin-authored ordinaire — même RBAC de lecture, même contenu
non-secret : image ref, protocoles, ressources). Si l'automatisation du
balayage d'orphelins devient un besoin plateforme, c'est un chantier
séparé qui toucherait `audit-orphans.sh` dans son ensemble, pas un cas
spécial pour ce type de ressource — ne l'anticipe pas ici.

**Exclusion du picker existant — condition nécessaire à "jamais
partagé"** : `TemplateService.List` (`api-server/internal/service/template_service.go:99-109`)
fait aujourd'hui un `s.kube.List` sans label selector dans
`s.namespace` — retourne TOUT, y compris les futurs templates
synthétiques. Cette liste alimente `GET /workspace-templates`
(`router.go:134-135`), une route accessible à tout utilisateur
authentifié, pas seulement aux admins. Sans filtre, un template
synthétique apparaîtrait dans le picker "créer depuis un template" de
n'importe quel utilisateur, et rien n'empêcherait alors un second
`Workspace` de le référencer via `templateRef` — cassant l'invariant
"immuable, jamais réédité, jamais partagé" énoncé plus haut (§
Mitigation watch/informer), dont dépendent à la fois le nettoyage à la
suppression ci-dessus (suppose 1 seul workspace référent) et la
lecture répétée du template à chaque reconcile par `buildPodTemplate`
(un second workspace perdrait son template si le premier est
supprimé). Ajoute un label selector excluant
`waas.xorhub.io/synthetic-template=true` dans `TemplateService.List` —
ne touche pas `TemplateService.Get` (utilisé par le chemin de création
interne, qui doit continuer à résoudre un template synthétique par
son nom exact).

### 4. Helm — RBAC delete

`helm/waas/templates/operator.yaml` : ajoute `delete` à
`workspacetemplates` (§ 3, nettoyage synthétique) — seul ajout RBAC de
ce volet, le reste (status, secrets) est déjà couvert par le volet A.

### 5. Frontend

- `GET /catalog` (`Governance.Catalog`, api-server) : ajoute
  `directDeployAllowed bool` (dérivé de `policy.DirectDeployAllowed`
  sur la policy résolue de l'appelant).
- `frontend/src/dialogs/CreateWorkspaceDialog.tsx` : **un seul bouton
  de création, pas deux entrées séparées "depuis un template" /
  "depuis le catalogue"**. Réutilise le composant de carte unifié du
  volet A (§ 6 de ce volet-là) : les entrées catalogue
  (`CatalogImage.discovered`) s'ajoutent comme cartes supplémentaires
  dans la MÊME grille que les templates, visibles seulement si
  `directDeployAllowed` — pas un mode/toggle séparé si la grille
  unifiée s'avère suffisamment claire à l'usage (carte catalogue vs
  carte template distinguées par une icône légère indiquant "pas de
  template dédié" ; à valider en test manuel avant de généraliser).
  Sélectionner une carte catalogue affiche protocole + taille dans les
  bornes de la `WorkspaceImage`, exactement comme pour un template
  (bornes déjà portées par `CatalogImage.min/max/defaults`,
  indépendamment de tout template — aucune nouvelle logique de calcul
  de sliders nécessaire, `clampRange` existant s'applique tel quel).
  Soumission → `POST /workspaces/direct` (nouveau hook
  `useCreateWorkspaceDirect`) au lieu de `POST /workspaces`.
- Aperçu de namespace et rattachement de volume retenu en mode
  catalogue : voir § Architecture ouverte — dépend de l'option
  retenue pour le placement.

## Contraintes

- Ne modifie AUCUNE ligne de `enforce()` au-delà du gate § 2 —
  `FindImage`/`ImageAllowed`/`CheckTagDiscipline`/`CheckProtocol`/
  `CheckOverrides`/`CheckLimits` restent des fonctions à un seul
  chemin, partagées, inchangées dans leur signature.
- Pas de bypass de code pour l'admin (§ Bypass admin) — seule la policy
  bootstrap change.
- Le template synthétique est immuable après création : aucun code ne
  doit jamais le `Update()` une fois créé (seule la suppression est un
  chemin valide).
- La résolution de la `WorkspaceImage` gouvernant une entrée choisie
  dans `CreateDirect` (§ 3 pt. 1) reste une correspondance par
  image/registre, pas une confiance aveugle dans quelle entrée
  `status.catalog` a produit `in.CatalogImage`.

## Tests

- `operator/pkg/policy` : tests unitaires `DirectDeployAllowed` (nil,
  false, true).
- `operator/internal/webhook/v1alpha1` : cas envtest — template
  synthétique + policy sans `directDeploy` ⇒ deny
  `DirectDeployNotAllowed` ; avec `directDeploy: true` ⇒ passe les
  gates habituels normalement.
- Predicate de filtrage sur `Watches(&WorkspaceTemplate{})` :
  `mapTemplateToWorkspaces` non invoqué pour un objet labellisé
  `synthetic-template=true` (test unitaire du predicate, pas besoin
  d'envtest complet).
- api-server : `CreateDirect` — image hors catalogue autorisé ⇒ 400
  avant toute création CR ; policy sans `directDeploy` ⇒ 403 ; succès
  ⇒ template + workspace créés, template supprimé si la création du
  workspace échoue ensuite.
- Suppression : workspace direct supprimé ⇒ son template synthétique
  disparaît (test operator, pas juste api-server, pour couvrir le
  chemin kubectl/GitOps direct).
- `hack/audit-orphans.sh` : cas template synthétique orphelin détecté.
- Frontend : test que les cartes catalogue apparaissent dans la grille
  unifiée seulement si `directDeployAllowed`, et que la soumission
  passe par `/workspaces/direct` pour une carte catalogue.
- `/verify` (skill du repo) sur le parcours complet si possible :
  policy `directDeploy: true`, création d'un workspace depuis une carte
  catalogue de bout en bout.

## Points ouverts (ton arbitrage)

- **Placement namespace** (§ Architecture ouverte) : Option 1 (accepter
  l'asymétrie + étendre `namespace-preview` sans template requis) vs
  Option 2 (`WorkspaceImageRef` + `Placement` sur `WorkspaceImage`,
  précédence à 4 paliers). Recommandation de départ : Option 1.
- Grille unifiée vs mode/toggle séparé pour le picker (§ 5 Frontend) —
  à valider en test manuel, les deux sont compatibles avec "un seul
  bouton de création".
- Nom exact de la route (`/workspaces/direct` proposé, libre de
  changer).
