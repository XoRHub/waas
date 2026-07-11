# Prompt Fable 5 — Feature 13 (volet waas-fable) : créer un workspace directement depuis un registre autorisé, sans WorkspaceTemplate

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable. Ce prompt couvre le repo `waas-fable` uniquement — le volet
`waas-images` (génération et publication des deux catalogues) est un
**prompt séparé et indépendant**, livré dans l'autre repo
(`docs/studies/prompt-feature13-catalog-publishing.md`). Les deux
volets se coordonnent uniquement via un format de fichier catalogue
partagé, spécifié en entier ci-dessous (§ Format du catalogue) — tu
n'as besoin de rien d'autre de l'autre repo pour livrer celui-ci : le
reconciler peut être développé et testé contre un fichier catalogue
fabriqué à la main respectant ce format, avant même que `waas-images`
ne publie quoi que ce soit.

## Contexte et objectif

Aujourd'hui, un `Workspace` référence toujours un `WorkspaceTemplate`
(`spec.templateRef`, obligatoire) admin-authored — c'est le seul
chemin de provisioning. L'objectif : permettre à un utilisateur
(si sa `WorkspacePolicy` l'y autorise) de créer un workspace en
choisissant directement une image dans un registre que l'admin a
approuvé **en entier** (`WorkspaceImage.spec.registry`, déjà
existant), sans qu'un admin ait dû créer un template pour cette image
précise au préalable. L'admin, lui, n'a besoin d'aucune autorisation
de policy particulière pour faire la même chose — nuance importante,
voir § Bypass admin ci-dessous.

En bonus, une routine périodique catalogue les images disponibles
dans les registres approuvés (os/app/version/icône) pour afficher un
logo à côté de chaque image dans le picker — réutilisable plus tard
par `WorkspaceTemplate`.

**Registres dans le périmètre de cette feature** : `ghcr.io/xorhub/waas-images`
et `docker.io/kasmweb` uniquement (les deux catalogues publiés par le
volet `waas-images`). Les registres privés arbitraires restent hors
périmètre — le mécanisme est générique (n'importe quelle
`WorkspaceImage` en mode registre peut porter un `catalogURL`), mais
rien n'exige de le documenter/promouvoir au-delà de ces deux registres
pour l'instant.

## Décision d'architecture centrale : synthétiser un WorkspaceTemplate, ne pas contourner l'enforcement existant

**Ne crée PAS de nouveau champ sur `Workspace.Spec`** (pas de
`imageRef`/`catalogImageRef`). `Workspace.Spec.TemplateRef` reste
l'unique pointeur de provisioning.

Raison : `enforce()` (`operator/internal/webhook/v1alpha1/workspace_webhook.go:354-424`),
`policy.LoadOf`/`policy.PlacementValues`/`policy.ResolvedDefaultNamespace`
(`operator/pkg/policy/policy.go`) et `buildPodTemplate`
(`operator/internal/controller/workload.go`) prennent TOUS un
`*WorkspaceTemplate` réel en paramètre et l'utilisent en profondeur
(image, OS, protocoles, ressources, placement, homeSize...). Dupliquer
ce chemin pour un provisioning "sans template" doublerait la
maintenance et rouvrirait exactement le risque de divergence que
`policy.OwnerLoads` documente déjà avoir corrigé une fois ("hand-copied
loops whose vanished-template fallbacks had silently diverged").

**Solution retenue** : quand l'utilisateur crée un workspace "depuis le
catalogue", l'api-server **synthétise un `WorkspaceTemplate` privé,
à usage unique**, 1:1 avec le futur workspace — `spec.image` = la
référence exacte choisie, `spec.protocols`/`spec.resources` dérivés du
choix utilisateur borné par la `WorkspaceImage` — puis appelle le
chemin de création de workspace **existant et inchangé** avec
`templateRef` pointant sur ce template synthétique. Toute la chaîne
d'enforcement/provisioning (webhook, quotas, `buildPodTemplate`,
placement) s'applique alors mot pour mot, sans code dupliqué.

Le template synthétique est marqué par un label (pas un nouveau champ
de schéma) :
- `waas.xorhub.io/synthetic-template: "true"`
- `waas.xorhub.io/owner: <owner UUID>` (label déjà existant,
  `operator/api/v1alpha1/identity.go:71`, réutilisé tel quel)

Ce marqueur sert à deux choses : (1) le webhook exige une autorisation
supplémentaire (`WorkspacePolicy.spec.directDeploy`) uniquement pour un
template ainsi marqué ; (2) le nettoyage à la suppression du workspace
(§ ci-dessous) sait quels templates supprimer.

## Bypass admin : pas de nouveau chemin de code

Le projet a une doctrine explicite et déjà appliquée : "the bypass
stays a VISIBLE, auditable WorkspacePolicy CR — never a code path"
(`helm/waas/values.yaml`, bloc `adminPolicy`). **Ne code PAS** de
bypass spécial "role admin ⇒ direct deploy autorisé" dans le webhook.
À la place :
- Le nouveau champ `WorkspacePolicy.spec.directDeploy` (booléen, voir
  ci-dessous) est un champ de policy comme un autre.
- La policy bootstrap tout-droits (`values.yaml` bloc `adminPolicy`,
  et `gitops/governance/policies.yaml`) doit inclure
  `directDeploy: true` — c'est la policy résolue pour l'admin qui porte
  le droit, exactement comme elle porte déjà l'absence de limites et
  le catalogue complet.
- L'admin garde par ailleurs le bypass générique existant
  (`operator.policyBypass`, groupes K8s type `system:masters`) pour les
  accès kubectl directs — inchangé, hors sujet ici.

Documente ce choix dans le commit/PR : c'est une clarification
délibérée par rapport à une lecture naïve de "l'admin n'a besoin
d'aucune autorisation" — dans ce système, TOUT le monde (admin inclus)
passe par une `WorkspacePolicy` résolue ; "aucune autorisation
particulière" se traduit par "la policy admin l'accorde par défaut",
pas par un branchement de code.

## Ce qu'il faut livrer

### 1. `WorkspacePolicy` — nouveau droit `directDeploy`

`operator/api/v1alpha1/workspacepolicy_types.go` — nouveau champ sur
`WorkspacePolicySpec`, juste après `RemoteWorkspaces` (même style,
même défaut fail-closed) :

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
`RemoteWorkspacesAllowed` (L618-620) :

```go
// DirectDeployAllowed reports whether the resolved policy opts its
// users into direct-from-catalog workspace creation. Nil policy = denied.
func DirectDeployAllowed(pol *waasv1alpha1.WorkspacePolicy) bool {
	return pol != nil && pol.Spec.DirectDeploy
}
```

Nouveau `Reason` dans le bloc `const` (L61-79) :
```go
ReasonDirectDeployNotAllowed Reason = "DirectDeployNotAllowed"
```

### 2. `WorkspaceImage` — catalogue fetché, registres waas-images + kasm uniquement

`operator/api/v1alpha1/workspaceimage_types.go` — sur
`WorkspaceImageSpec`, après `Resources` :

```go
// CatalogURL points at a published catalog manifest (format below)
// listing the images currently under this entry's registry, with
// display metadata (os/app/version/icon) for the portal catalog
// picker. Only meaningful when spec.registry is set (ignored on exact
// spec.image entries — ENFORCEMENT never reads this field, it is
// purely cosmetic). Absent = no automatic catalog; the registry
// approval itself still works.
// +optional
CatalogURL string `json:"catalogURL,omitempty"`
```

Nouveau statut :

```go
// DiscoveredImage is one entry surfaced by a registry-mode
// WorkspaceImage's catalog sync — display metadata only, NEVER
// consulted by policy/enforcement (that stays FindImage/ImageAllowed
// against spec.image/spec.registry, unchanged).
type DiscoveredImage struct {
	// Image is the exact, pinned reference (digest recommended).
	Image string `json:"image"`
	// +optional
	OS OSType `json:"os,omitempty"`
	// App is a logical grouping slug (e.g. "firefox", "ubuntu-xfce") —
	// distinct images of the same app across versions share it.
	// +optional
	App string `json:"app,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
	// Icon is a dashboard-icons (github.com/homarr-labs/dashboard-icons,
	// Apache-2.0) slug, e.g. "firefox". The frontend resolves it against
	// a LOCALLY VENDORED subset (see § 7) — never fetched live from
	// GitHub — falling back to an OS icon when absent or unknown.
	// +optional
	Icon string `json:"icon,omitempty"`
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

type WorkspaceImageStatus struct {
	// Catalog is the last known set of discovered images (registry-mode
	// entries only). Stale-but-served: a failed sync never clears it.
	// +optional
	Catalog []DiscoveredImage `json:"catalog,omitempty"`
	// CatalogSource says where Catalog came from: "Fetched" (a live sync
	// succeeded, ever) or "Bundled" (seeded from the operator's embedded
	// snapshot because no fetch has EVER succeeded — airgap day-0 case).
	// Empty = never synced and nothing bundled for this entry.
	// +optional
	CatalogSource string `json:"catalogSource,omitempty"`
	// LastSyncTime is when Catalog was last written: the real sync time
	// for "Fetched", the operator's build date for "Bundled" (so an
	// admin can tell a permanently-airgapped catalog is stale-by-design).
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// LastSyncError is the most recent fetch failure, kept even after a
	// later fallback-to-bundled succeeds, so admins can see WHY.
	// +optional
	LastSyncError string `json:"lastSyncError,omitempty"`
}
```

Add `// +kubebuilder:subresource:status` to the `WorkspaceImage` root
marker (it doesn't have one today — check
`operator/api/v1alpha1/workspaceimage_types.go:146-153` before adding;
this is a genuinely additive, backward-compatible change per
[ADR 0002](../adr/0002-crd-evolution.md), but confirm no existing code
assumes status is absent/unused on this type before wiring the
subresource — grep `WorkspaceImage{}.Status` and `.Status =` across
`operator/` and `api-server/` first).

### 3. Format du catalogue (contrat partagé avec le repo waas-images)

Fichier YAML, une version de format explicite pour ne jamais
mésinterpréter silencieusement un futur changement :

```yaml
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/ubuntu-xfce:1.1.0@sha256:...
    os: linux
    app: ubuntu-xfce
    version: "1.1.0"
    icon: linux
    displayName: "Ubuntu 24.04 — XFCE"
  - image: ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:...
    os: linux
    app: firefox
    version: "1.0.0"
    icon: firefox
```

Un parseur tolérant : `apiVersion` inconnue ⇒ rejette proprement
(erreur de sync, PAS un crash), champs optionnels absents ⇒ zéro
valeur (`os` vide se traite comme `linux` côté frontend, pas une
erreur).

### 4. `WorkspaceImageReconciler` — nouveau controller

`WorkspaceImage` n'a aujourd'hui AUCUN reconciler (objet passif, lu
seulement par le webhook/policy) — c'est le premier. Fichier suggéré :
`operator/internal/controller/workspaceimage_catalog.go`, modèle à
suivre : `namespace_janitor.go` pour la forme d'un `Reconciler`
indépendant + `workspace_controller.go` pour le pattern de self-requeue
(`ctrl.Result{RequeueAfter: interval}`).

Logique par `WorkspaceImage` dont `spec.registry != "" && spec.catalogURL != ""` :
1. `GET` HTTP simple (`net/http`, pas de nouvelle dépendance — PAS
   `crane`/`go-containerregistry`, le catalogue est un fichier statique,
   pas un scan de registre) sur `spec.catalogURL`, timeout raisonnable
   (5-10s).
2. Succès + parse OK → `status.catalog` = le contenu parsé,
   `status.catalogSource = "Fetched"`, `status.lastSyncTime = now`,
   `status.lastSyncError = ""`.
3. Échec (réseau, HTTP non-200, parse) → **ne jamais vider un
   `status.catalog` déjà peuplé** (stale-but-served) ; `status.lastSyncError`
   mis à jour dans tous les cas. Si `status.catalog` est vide ET qu'un
   snapshot embarqué existe pour ce nom d'entrée (§ 5) → seed depuis le
   snapshot, `status.catalogSource = "Bundled"`.
4. Requeue après `RequeueAfter: interval` (succès ou échec — même
   cadence). `interval` vient d'une variable d'env opérateur (voir § 8).
5. Watch sur `WorkspaceImage` : une édition de `spec.catalogURL`
   retrigger immédiatement (pas besoin d'attendre le prochain requeue).

Ce fetch ne bloque JAMAIS la création de workspace : c'est un
reconciler séparé, purement cosmétique, `enforce()`/`FindImage` ne le
lisent jamais.

### 5. Fallback embarqué (airgap day-0)

Précédent direct à suivre : `kasmvnc_defaults.yaml` embarqué via
`//go:embed` (`operator/internal/controller/kasm_config.go` — lis-le
pour la convention exacte). Nouveau :
`operator/internal/catalog/embedded/` avec deux fichiers au format
§ 3 : `waas-images.yaml` et `kasmweb.yaml`, mis à jour à la main à
chaque release de l'opérateur (pas d'automatisation ici — c'est un
snapshot figé au moment du build, documenté comme tel dans un
commentaire au sommet de chaque fichier). Le reconciler doit savoir
associer une `WorkspaceImage` à SON snapshot embarqué — le plus simple
est un mapping par `spec.registry` exact (`ghcr.io/xorhub/waas-images`
→ `waas-images.yaml`, `docker.io/kasmweb` → `kasmweb.yaml`) codé en dur
dans le reconciler ; une `WorkspaceImage` avec un autre registre +
`catalogURL` n'a simplement pas de fallback (comportement : reste vide
tant qu'aucun fetch ne réussit, ce qui est correct — le fallback
embarqué n'a de sens que pour les deux registres que la plateforme
connaît par construction).

### 6. Webhook — nouveau gate pour template synthétique

`operator/internal/webhook/v1alpha1/workspace_webhook.go`, fonction
`enforce()` (L354) : juste avant ou après `policy.Resolve` (L370),
ajoute :

```go
if tpl.Labels["waas.xorhub.io/synthetic-template"] == "true" && !policy.DirectDeployAllowed(pol) {
	return warnings, &policy.Denial{Reason: policy.ReasonDirectDeployNotAllowed,
		Message: fmt.Sprintf("policy %q does not grant direct-from-catalog deployment", pol.Name)}
}
```

(Ajuste l'ordre exact selon où `pol` est disponible — `Resolve` doit
avoir tourné avant.) Le reste de `enforce()` — `FindImage`,
`ImageAllowed`, `CheckTagDiscipline`, `CheckProtocol`, `CheckOverrides`,
`CheckLimits` — s'applique SANS modification : c'est tout le bénéfice
du template synthétique réel.

Ajoute la constante de label dans `operator/api/v1alpha1/identity.go`
(à côté de `LabelOwner`, `LabelRetained`, L71/L80) :
```go
LabelSyntheticTemplate = "waas.xorhub.io/synthetic-template"
```

### 7. api-server — synthèse du template + nouvel endpoint

Nouvelle route, `api-server/internal/server/router.go` (à côté de
`/workspace-templates`, L126) :

```go
r.Post("/workspaces/direct", h.Workspaces.CreateDirect)
```

`CreateWorkspaceInput`-like struct dédiée (nouveau type dans
`api-server/internal/service/`), champs : `catalogImage string`
(référence exacte choisie, doit apparaître dans
`WorkspaceImage.status.catalog[].image` d'une entrée pour laquelle
`DirectDeployAllowed` + `policy.Images` l'autorisent — le service DOIT
revérifier ces deux points côté api-server aussi, en plus du webhook :
message d'erreur clair avant même de tenter la création, cohérent avec
comment `WorkspaceService.Create` valide déjà `templateRef` en amont,
L184-196), `protocol string`, `resources *corev1.ResourceRequirements`,
`displayName string`.

Logique du nouveau `WorkspaceService.CreateDirect` :
1. Résout la `WorkspaceImage` propriétaire de l'entrée catalogue
   choisie (celle dont `status.catalog[].image == in.CatalogImage`).
2. Construit un `*waasv1alpha1.WorkspaceTemplate` en mémoire :
   `Name` généré (ex. `direct-<uuid court>`, même discipline que
   `generateWorkspaceName` existant), `Labels: {synthetic-template: "true", owner: ownerID}`,
   `Spec.OS` = celui de l'entrée catalogue (défaut `linux`),
   `Spec.Image` = `in.CatalogImage`, `Spec.Protocols` dérivé de
   `WorkspaceImage.Spec.Protocols` ∩ `in.Protocol`, `Spec.Resources`
   = `in.Resources` ou le défaut de la `WorkspaceImage`.
   **`Spec.Overrides` reste nil** : ce template n'est jamais réutilisé
   ni partagé, la délégation d'override n'a pas de sens ici — tout ce
   que l'utilisateur veut est écrit directement dans le spec synthétisé.
3. `s.kube.Create` ce template (le SA api-server a déjà `create` sur
   `workspacetemplates`, `helm/waas/templates/api-server.yaml:19` —
   aucun nouveau RBAC).
4. Appelle exactement le même chemin que `WorkspaceService.Create`
   (L172+) avec `TemplateRef` = le nom généré — factorise plutôt que
   dupliquer (extrait la partie "à partir d'un template déjà résolu"
   de `Create` en fonction interne partagée par les deux entrypoints).
5. Si la création du `Workspace` échoue APRÈS que le template ait été
   créé, supprime le template synthétique avant de renvoyer l'erreur
   (pas de résidu sur un échec).

**Nettoyage à la suppression** : quand un `Workspace` est supprimé, si
son `templateRef` pointe vers un template portant
`labels[synthetic-template]=true`, supprime aussi ce template. Regarde
`operator/internal/controller/workspace_teardown_test.go` pour le point
d'accroche exact (reconciler operator, pas api-server — le workspace
peut aussi être supprimé par kubectl/GitOps direct, donc le nettoyage
DOIT vivre côté operator, pas seulement dans le endpoint API DELETE).

**Audit orphelins** : `hack/audit-orphans.sh` ne couvre aujourd'hui pas
`WorkspaceTemplate` (recherche `grep -n workspacetemplate` — zéro
résultat, confirmé). Étends-le : un template `synthetic-template=true`
sans workspace vivant le référençant est un orphelin, à détecter comme
le reste du sweep. Ne touche pas au comportement pour les templates
admin-authored (jamais orphelins par construction).

### 8. Helm — intervalle de sync, RBAC status

`helm/waas/values.yaml` : nouvelle clé `operator.catalogSyncInterval`
(défaut `6h`, même esprit que `apiServer.eventsPollInterval`). Câblée
en variable d'env opérateur, lue par le nouveau reconciler.

RBAC : `helm/waas/templates/operator.yaml:67` a déjà
`workspacetemplates, workspaceimages, workspacepolicies` — ajoute
`workspaceimages/status` en `update;patch` si pas déjà couvert par le
verbe générique (vérifie ce que `+kubebuilder:rbac` génère une fois le
marker de subresource status ajouté, régénère avec `make manifests`).

### 9. Frontend

- `GET /catalog` (`Governance.Catalog`, api-server) : ajoute
  `directDeployAllowed bool` (dérivé de `policy.DirectDeployAllowed`
  sur la policy résolue de l'appelant) + les entrées
  `WorkspaceImage.status.catalog` des images de catalogue autorisées
  (réutilise `policy.AllowedImages` pour le filtrage — même gate que
  le reste).
- `frontend/src/dialogs/CreateWorkspaceDialog.tsx` : second mode
  "Depuis un catalogue", visible seulement si `directDeployAllowed`.
  Sélection d'une entrée catalogue (image+logo), puis protocole +
  taille dans les bornes de la `WorkspaceImage` — même esprit que le
  picker de template existant (L62, L156, L268-271), pas un nouveau
  design pattern.
- Nouveau composant de résolution d'icône, ex.
  `frontend/src/lib/icon.ts` : `resolveIcon(slug?: string, os?: string): string`
  — retourne le chemin d'un asset vendoré localement
  (`/icons/<slug>.svg`), fallback sur `/icons/os-<linux|windows>.svg`
  si `slug` absent ou pas vendoré. **Aucun fetch réseau à l'exécution**
  (même raisonnement airgap que le catalogue lui-même — un navigateur
  en environnement isolé ne peut pas non plus taper une CDN externe).
  Utilisé par `SessionCard`/`WorkspaceCard`
  (`frontend/src/components/SessionCard.tsx`,
  `frontend/src/sections/WorkspacesSection.tsx:70-182`) et le nouveau
  picker.
- Vendoring des icônes : `dashboard-icons`
  (github.com/homarr-labs/dashboard-icons, Apache-2.0, structure
  `svg/<slug>.svg`, licence compatible avec la simple copie + mention
  d'attribution) — copie manuelle/scriptée d'un sous-ensemble
  (uniquement les slugs référencés par les deux catalogues connus +
  `linux`/`windows` en fallback) dans `frontend/public/icons/`, avec
  un fichier `frontend/public/icons/ATTRIBUTION.md` citant la licence
  Apache-2.0 et le repo source. Rafraîchi occasionnellement à la main
  (pas de pipeline de sync automatique dans le périmètre de cette
  feature) — ajoute un court script `hack/vendor-icons.sh` qui prend
  une liste de slugs et les télécharge depuis le CDN jsDelivr
  (`https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/<slug>.svg`)
  pour faciliter les rafraîchissements futurs, mais NE l'exécute PAS à
  l'exécution de l'app.

### 10. Régénération

`make manifests generate docs-params generate-types` — CRD YAML
(`helm/waas/crds/waas.xorhub.io_workspaceimages.yaml`,
`waas.xorhub.io_workspacepolicies.yaml`), types TS générés
(`frontend/src/types.gen.ts`), doc des paramètres si le registre de
protocole est touché (probablement non ici).

## Contraintes

- [ADR 0002](../adr/0002-crd-evolution.md) : additif uniquement, aucun
  champ renommé/retypé, `v1alpha1` reste `v1alpha1`.
- Ne modifie AUCUNE ligne de `enforce()` au-delà du gate § 6 —
  `FindImage`/`ImageAllowed`/`CheckTagDiscipline`/`CheckProtocol`/
  `CheckOverrides`/`CheckLimits` doivent rester des fonctions à un seul
  chemin, partagées, inchangées dans leur signature.
- Pas de bypass de code pour l'admin (§ Bypass admin) — seule la policy
  bootstrap change.
- Le fetch catalogue ne doit JAMAIS pouvoir bloquer ou retarder la
  création/l'usage d'un workspace — c'est un reconciler séparé, pas un
  appel synchrone dans `enforce()` ou `buildPodTemplate`.
- Aucune nouvelle dépendance Go pour le fetch (pas de
  `go-containerregistry`/`crane` — un `GET` HTTP suffit, le catalogue
  est un fichier statique publié par CI, pas un registre à scanner).

## Tests

- `operator/pkg/policy` : tests unitaires `DirectDeployAllowed` (nil,
  false, true).
- `operator/internal/webhook/v1alpha1` : cas envtest — template
  synthétique + policy sans `directDeploy` ⇒ deny
  `DirectDeployNotAllowed` ; avec `directDeploy: true` ⇒ passe les
  gates habituels normalement (catalogue/quota s'appliquent toujours).
- Nouveau reconciler : fetch OK, fetch KO avec catalogue déjà peuplé
  (stale-but-served), fetch KO day-0 avec/sans snapshot embarqué
  (bundled vs vide), parse d'un `apiVersion` inconnue (rejet propre).
- api-server : `CreateDirect` — image hors catalogue autorisé ⇒ 400
  avant toute création CR ; policy sans `directDeploy` ⇒ 403 ; succès
  ⇒ template + workspace créés, template supprimé si la création du
  workspace échoue ensuite.
- Suppression : workspace direct supprimé ⇒ son template synthétique
  disparaît (test operator, pas juste api-server, pour couvrir le
  chemin kubectl/GitOps direct).
- `hack/audit-orphans.sh` : cas template synthétique orphelin détecté.
- Frontend : Vitest sur `resolveIcon` (slug connu, slug inconnu,
  absent, fallback OS) ; test du nouveau mode du dialog masqué/affiché
  selon `directDeployAllowed`.
- `/verify` (skill du repo) sur le parcours complet si possible :
  policy `directDeploy: true` + `WorkspaceImage` registre avec
  `catalogURL` pointant un fichier de test local (ex. servi par un
  petit serveur HTTP dans le test, ou un fichier `file://` si le
  reconciler le supporte — sinon un serveur `httptest.Server` suffit
  pour les tests Go, la vérification manuelle en dev peut pointer vers
  un fichier hébergé temporairement).

## Points ouverts (ton arbitrage)

- Nom exact de la route/du endpoint (`/workspaces/direct` proposé,
  libre de changer si un nom plus clair s'impose une fois le code sous
  les yeux).
- Faut-il exposer `WorkspaceImage.status.catalogSource`/`lastSyncError`
  dans `kubectl get workspaceimage -o wide` via un printcolumn ? Utile
  pour le debug admin, pas strictement nécessaire — à ta discrétion.
- Le script `hack/vendor-icons.sh` : niveau de sophistication libre
  (liste de slugs en dur vs argument CLI) — ce n'est pas un chemin
  d'exécution runtime, la barre est basse.
