# Prompt Fable 5 — Feature 13 v2, volet A (waas-fable) : catalogue d'images publié (`WorkspaceImage.status.catalog`) et picker visuel unifié

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable. **Ce document remplace intégralement**
`docs/studies/17-prompt-feature13-direct-image-deploy-waas.md`, gardé
en archive pour l'historique de décision — ne l'utilise pas comme
source, une relecture architecte a changé la conception du catalogue
(§ Décision d'architecture centrale ci-dessous détaille quoi et
pourquoi).

**Ce document est le volet A d'une feature scindée en deux prompts
indépendants** après relecture architecte : la feature 13 v2 couvrait à
l'origine à la fois le catalogue d'images ET la création d'un workspace
sans `WorkspaceTemplate` admin-authored ("orphelin"). Ces deux besoins
ont des rayons d'impact très différents — le catalogue ne touche jamais
`enforce()`/le provisioning, la création orpheline si — et la question
du placement namespace pour un workspace orphelin (pas de template =
pas de pattern de placement custom) s'est révélée être une vraie
question d'architecture à part entière. D'où le split :
- **Volet A (ce document)** : le catalogue lui-même — fetch, statut,
  schéma JSON, visibilité, picker visuel. Livrable et utile seul, y
  compris pour améliorer le picker de templates EXISTANT (logos), sans
  jamais dépendre du volet B.
- **Volet B** (`docs/studies/28-prompt-feature13-direct-deploy-orphan-workspace.md`) :
  la création orpheline elle-même (`WorkspacePolicy.directDeploy`,
  synthèse de `WorkspaceTemplate`, endpoint `CreateDirect`, gate
  webhook). **Dépend de ce volet A** (consomme
  `status.catalog.entries` pour son picker) — l'inverse n'est pas vrai.

Ce prompt couvre le repo `waas-fable` uniquement — le volet
`waas-images` (génération et publication des deux catalogues) reste un
**prompt séparé et indépendant**. Les deux volets (repos) se
coordonnent uniquement via le format de fichier catalogue partagé,
spécifié en entier ci-dessous (§ 1 Format du catalogue) — inchangé par
rapport à la v1, tu n'as besoin de rien d'autre de l'autre repo.

## Contexte et objectif

**Cataloguer les images approuvées** (os/app/version/icône) pour
afficher un picker avec logos plutôt qu'une liste de références brutes
— un fetch périodique d'un fichier `catalog.yaml` publié par le
registre, exposé sur `WorkspaceImage.status`.

**Registres dans le périmètre** : `ghcr.io/xorhub/waas-images` et
`docker.io/kasmweb` uniquement (les deux catalogues publiés par le
volet `waas-images`). Le mécanisme reste générique (n'importe quelle
`WorkspaceImage` en mode registre peut porter un `spec.catalog`), mais
rien n'exige de le documenter/promouvoir au-delà de ces deux registres.

## Décision d'architecture centrale : le catalogue vit DANS WorkspaceImage, pas dans une CR séparée

**Écart volontaire par rapport à un premier brouillon** qui proposait
une CR `WorkspaceCatalog` séparée référencée par
`WorkspaceImage.spec.catalogRef`. Arbitrage retenu après examen :
`WorkspaceCatalog` aurait toujours été en cardinalité 1:1 stricte avec
une `WorkspaceImage` (jamais d'agrégation multi-registre, jamais de vue
curatée distincte de l'approbation — ces deux besoins ont été
explicitement écartés), et son seul bénéfice réel (permettre un jour
un rôle "éditeur de catalogue" distinct de l'admin sécurité via un
RBAC K8s scopé à une ressource différente) ne tient pas : le modèle
d'autorisation de toute la plateforme est déjà **applicatif, pas du
RBAC K8s natif** (`WorkspacePolicy` elle-même n'est enforcée par aucun
verbe RBAC sur `Workspace`, uniquement par le webhook/api-server qui
résout une policy). Un futur rôle "éditeur de catalogue" serait de
toute façon vérifié dans le code de l'api-server avant le
`PATCH spec.catalog`, exactement comme tout le reste de la
gouvernance — une CR séparée n'aurait rien apporté à ce futur rôle,
juste dupliqué un objet pour une cardinalité qui reste 1:1 par
construction.

**Conséquence pratique** : si un vrai besoin de séparation RBAC K8s
apparaît un jour (accès direct kubectl/GitOps à granularité fine), le
split reste possible plus tard — grouper les champs sous une struct
dédiée maintenant (au lieu de champs plats) rend ce split mécanique
plus tard. C'est pour ça que `Catalog` est un struct imbriqué et non
des champs plats sur `WorkspaceImageSpec`/`WorkspaceImageStatus`.

`operator/api/v1alpha1/workspaceimage_types.go` — sur
`WorkspaceImageSpec`, après `Resources` :

```go
// Catalog configures the periodic fetch of a published catalog
// manifest (format below) listing the images currently under this
// entry's registry, with display metadata (os/app/version/icon) for
// the portal catalog picker. Only meaningful when spec.registry is
// set (ignored on exact spec.image entries — ENFORCEMENT never reads
// this field, it is purely cosmetic). Grouped under one struct
// (rather than flat fields) so a future split into its own CRD, if a
// real need for K8s-RBAC-level separation ever appears, is a
// mechanical lift instead of a field-by-field migration — no such
// split is planned today, application-level authorization (the same
// model WorkspacePolicy already uses) covers any future need for a
// narrower "catalog editor" role. Absent = no automatic catalog; the
// registry approval itself still works.
// +optional
Catalog *ImageCatalogSpec `json:"catalog,omitempty"`
```

```go
// ImageCatalogSpec points at exactly one catalog manifest source.
type ImageCatalogSpec struct {
	// From is the catalog manifest source (format below, § 1) —
	// exactly one of URL/ConfigMapKeyRef/SecretKeyRef, mutually
	// exclusive (enforced on ImageCatalogSource below; never more than
	// one set). URL is fetched live over HTTP(S); ConfigMapKeyRef/SecretKeyRef
	// are read directly, no HTTP involved. Both are re-checked on the
	// SAME periodic cadence (operator.catalogSyncInterval, § 7) — no
	// dedicated watch on the referenced ConfigMap/Secret (§ 4 explains
	// why) — a static, GitOps-managed catalog for an admin who prefers
	// not to depend on a live registry endpoint. This is a first-class,
	// permanent choice, not a stopgap-until-network-works: an admin
	// picks ONE of the three and stays on it.
	// +kubebuilder:validation:Required
	From ImageCatalogSource `json:"from"`

	// Auth configures how the live fetch authenticates — only
	// meaningful when From.URL is set (ignored, and rejected at
	// admission if From points at ConfigMapKeyRef/SecretKeyRef instead,
	// see the XValidation on ImageCatalogSpec below the struct).
	// Nested by method (one field per auth kind) instead of a flat
	// credential reference, so a future method (basic auth, mTLS...) is
	// a pure ADDITION — a new sibling field on ImageCatalogAuth — never
	// a rename or a reinterpretation of what an existing field means.
	// Absent = unauthenticated GET, the only mode the two known public
	// catalogs (ghcr.io/xorhub/waas-images, docker.io/kasmweb) need.
	// +optional
	Auth *ImageCatalogAuth `json:"auth,omitempty"`
}
```

`+kubebuilder:validation:XValidation:rule="!has(self.auth) || self.from.url != ''",message="auth is only meaningful when from.url is set"`
on `ImageCatalogSpec` — couples `Auth` to the URL variant explicitly instead of
silently ignoring it (fail-soft doctrine covers runtime data issues, not a
config that can never do anything).

```go
// ImageCatalogSource names the catalog manifest source — exactly one
// of URL/ConfigMapKeyRef/SecretKeyRef must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.url) ? 1 : 0) + (has(self.configMapKeyRef) ? 1 : 0) + (has(self.secretKeyRef) ? 1 : 0) == 1",message="exactly one of url, configMapKeyRef, or secretKeyRef must be set"
type ImageCatalogSource struct {
	// URL is the catalog manifest location, fetched live and
	// periodically (§ 7 interval).
	// +optional
	URL string `json:"url,omitempty"`

	// ConfigMapKeyRef reads the manifest from a ConfigMap key in the
	// platform workspace namespace instead of fetching it over HTTP —
	// the common case, since the content isn't secret, just a static
	// admin-provided catalog. Key defaults to "catalog.yaml" when
	// empty. Re-read periodically (operator.catalogSyncInterval, § 7),
	// not just once — no dedicated watch on the ConfigMap (§ 4 explains
	// why).
	// +optional
	ConfigMapKeyRef *corev1.ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`

	// SecretKeyRef reads the manifest from a Secret key instead, in the
	// platform workspace namespace — for an admin who wants the
	// manifest content itself access-controlled. Key is REQUIRED (no
	// default, unlike ConfigMapKeyRef): no naming convention is assumed
	// for a Secret. Distinct from ImageCatalogAuth.BearerToken below:
	// that one is a fetch CREDENTIAL for a URL source, this one IS the
	// manifest content itself; the two are never the same Secret in
	// practice but nothing in the schema prevents it, and they cannot
	// both apply at once since URL/SecretKeyRef are mutually exclusive.
	// +optional
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}
```

```go
// ImageCatalogAuth holds one authentication method for the catalog
// fetch. Only BearerToken exists today — deliberately no
// mutual-exclusion XValidation yet (a single optional field has
// nothing to conflict with; that CEL rule is dead code until a second
// method exists). Add the "exactly one of" rule THE DAY a second
// method is introduced, not before (YAGNI).
type ImageCatalogAuth struct {
	// BearerToken sends "Authorization: Bearer <token>" on the fetch.
	// +optional
	BearerToken *BearerTokenAuth `json:"bearerToken,omitempty"`
}

// BearerTokenAuth names the Secret holding the bearer token used to
// authenticate the catalog fetch.
type BearerTokenAuth struct {
	// SecretRef names an existing Opaque Secret (in the platform
	// workspace namespace, same convention as
	// WorkspaceImageSpec.ImagePullSecretRef) holding the token under
	// the key "token". A missing/unreadable Secret, or one without this
	// key, is a sync failure (status.catalog.lastSyncError), never a
	// crash — same fail-soft doctrine as the rest of the reconciler
	// (see § 4).
	// +kubebuilder:validation:MinLength=1
	SecretRef string `json:"secretRef"`
}
```

Nouveau statut — `WorkspaceImage` n'a AUCUN `Status` aujourd'hui
(grep `Status` sur `workspaceimage_types.go` : zéro résultat), donc
c'est un ajout net, pas une extension :

```go
// DiscoveredImage is one entry surfaced by a catalog sync — display
// metadata only, NEVER consulted by policy/enforcement (that stays
// FindImage/ImageAllowed against spec.image/spec.registry, unchanged).
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
	// a LOCALLY VENDORED subset (see § 6) — never fetched live from
	// GitHub — falling back to an OS icon when absent or unknown.
	// +optional
	Icon string `json:"icon,omitempty"`
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

// ImageCatalogStatus is the last known result of the catalog fetch
// configured by spec.catalog.
type ImageCatalogStatus struct {
	// Entries is the last known set of discovered images. Stale-but-served:
	// a failed sync never clears it.
	// +optional
	Entries []DiscoveredImage `json:"entries,omitempty"`
	// Source says which From variant produced Entries: "Fetched"
	// (From.URL, a live sync succeeded at least once) or "Static"
	// (From.ConfigMapKeyRef/SecretKeyRef was read successfully at least
	// once). Empty = never synced yet.
	// +optional
	Source string `json:"source,omitempty"`
	// LastSyncTime is when Entries was last written: the real fetch
	// time for "Fetched", the time the ConfigMap/Secret was last read
	// for "Static".
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// LastSyncError is the most recent fetch/read failure, kept even
	// after a later sync succeeds, so admins can see WHY it once
	// failed.
	// +optional
	LastSyncError string `json:"lastSyncError,omitempty"`
}

type WorkspaceImageStatus struct {
	// Catalog is nil until the first sync attempt of a spec.catalog-configured
	// entry.
	// +optional
	Catalog *ImageCatalogStatus `json:"catalog,omitempty"`
}
```

Add the `Status` field to the `WorkspaceImage` root struct (today it
only has `Spec`) and `+kubebuilder:subresource:status` to its root
marker block:

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wsi
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registry`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Catalog",type=string,JSONPath=`.status.catalog.source`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type WorkspaceImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceImageSpec   `json:"spec"`
	Status WorkspaceImageStatus `json:"status,omitempty"`
}
```

Ceci est additif et rétro-compatible per
[ADR 0002](../adr/0002-crd-evolution.md) — mais avant d'ajouter le
subresource, grep `WorkspaceImage{}.Status` et `.Status =` across
`operator/` et `api-server/` pour confirmer qu'aucun code existant
n'assume l'absence de status sur ce type (attendu : rien, l'objet est
purement lu par le webhook aujourd'hui).

### Pourquoi ce merge est sûr vis-à-vis du watch existant sur WorkspaceImage

`workspace_controller.go` a AUSSI un
`Watches(&waasv1alpha1.WorkspaceImage{}, mapCatalogToWorkspaces,
predicate.GenerationChangedPredicate{})` — édition d'une entrée
catalogue (arch affinity, pull secret) → réévaluation drift de toute
la flotte du namespace. Une fois `+kubebuilder:subresource:status`
ajouté, les écritures de `status.catalog.*` passent par le sous-ressource
`Status().Patch()` et **ne bump JAMAIS `metadata.generation`** — donc
le sync périodique du catalogue (toutes les 6h par défaut) ne
déclenche jamais ce watch. Seule une édition humaine de
`spec.catalog.from`/`auth` bump la génération et redéclenche
`mapCatalogToWorkspaces` pour toute la flotte — comportement déjà
accepté aujourd'hui pour toute édition de `WorkspaceImage.spec`
("catalog edits are rare admin operations and an in-sync reconcile is
a cheap no-op", commentaire existant). Rien de nouveau à mitiger ici.

Les deux `Watches(&WorkspaceImage{}, ...)` (celui de
`workspace_controller.go` pour le drift, et le nouveau reconciler du
§ 4 pour le fetch catalogue) sont deux controllers indépendants
observant le même GVK pour des raisons différentes — pattern normal en
controller-runtime, aucun conflit.

## Ce qu'il faut livrer

### 1. Format du catalogue (contrat partagé avec le repo waas-images) — inchangé

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

Parseur tolérant : `apiVersion` inconnue ⇒ rejette proprement (erreur
de sync, PAS un crash), champs optionnels absents ⇒ zéro valeur (`os`
vide se traite comme `linux` côté frontend, pas une erreur).

### 2. Schéma JSON versionné du catalogue — waas-fable en est l'unique source de vérité

waas-fable est le **lecteur** du format catalogue (§1) : c'est donc lui
qui fait autorité sur le schéma, pas waas-images (le producteur). Le
schéma existe **UNIQUEMENT dans ce repo** — jamais dupliqué ni
vendoré côté waas-images ni ailleurs — et est référencé depuis les
fichiers `catalog.yaml` par une URL HTTPS pointant ce repo, consommée
par un yaml-language-server (ex. extension `redhat.vscode-yaml`) pour
la validation/autocomplétion à l'édition :

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/xorhub/waas-fable/<tag>/operator/pkg/catalog/schema/v1.schema.json
apiVersion: waas.xorhub.io/catalog/v1
images: ...
```

`<tag>` = un tag de release waas-fable, jamais `main` — même discipline
que le reste du projet contre les références mouvantes (pas de
`:latest`). Cette URL n'est qu'un confort éditeur pour quiconque édite
un `catalog.yaml` à la main (côté waas-images ou ailleurs) : le fetch
HTTPS a lieu dans l'éditeur de la personne qui édite, jamais dans le
reconciler waas-fable ni dans un chemin runtime — `status.catalog` reste
alimenté uniquement par le parseur Go tolérant décrit ci-dessus,
indépendamment de ce schéma. Une validation CI côté waas-images (si ce
repo veut gater sa publication dessus) est une décision qui lui est
propre, hors périmètre de ce prompt.

**Le schéma n'est pas écrit à la main : il est généré depuis le struct
Go canonique**, pour qu'il ne puisse jamais diverger silencieusement du
parseur réel.

- `operator/pkg/catalog` (nouveau package) : struct Go du wire-format —
  `File{APIVersion string; Images []Entry}` — **distinct de
  `DiscoveredImage`** (§ Décision d'architecture centrale ; les
  deux types ont des cadences de compatibilité différentes, l'un suit
  le cycle CRD `v1alpha1`/ADR 0002, l'autre un contrat de fichier
  inter-repo indépendant). Ce struct est celui que le parseur du
  reconciler (§4) `Unmarshal` réellement — une seule définition, deux
  consommateurs (parseur + générateur de schéma), donc aucune fixture
  de synchronisation à maintenir séparément.
- `operator/hack/gen-catalog-schema/main.go` : petite commande locale
  import `catalog.File` + une lib de génération de JSON Schema par
  réflexion (ex. `invopop/jsonschema`), écrit
  `operator/pkg/catalog/schema/v1.schema.json`. Dépendance ajoutée à
  `operator/go.mod` mais jamais importée par `cmd/operator` — même
  pattern que `tygo` pour `generate-types` (`Makefile:57`, invoqué en
  `go run pkg@version`, aucune entrée `go.mod` requise pour celui-là
  précisément, mais même esprit d'outil de génération non lié au
  binaire livré).
- Un fichier par `apiVersion` (`v1.schema.json`), figé une fois publié —
  même discipline additive que les CRD.
- **Câblage make/CI, réutilise le mécanisme existant, n'en crée pas un
  nouveau** :
  - Ajoute la génération au target `generate` existant
    (`Makefile:45-46`).
  - Étends la liste de chemins du `git diff --exit-code` du job
    `go-generated-drift` (`.github/workflows/ci.yml:283-284`) pour
    inclure `operator/pkg/catalog/schema` — exactement le mécanisme qui
    gate déjà la dérive des CRD/types TS, un chemin de plus dans le
    même appel, pas un nouveau job.

### 3. Visibilité du catalogue : héritée, pas de nouveau champ

`status.catalog.entries` d'une `WorkspaceImage` n'est visible dans
l'API `/catalog` (§ 6) que pour les utilisateurs que
`policy.AllowedImages`/`WorkspaceImage.spec.AllowedGroups` autorisent
déjà pour CETTE `WorkspaceImage` — même porte que l'enforcement, aucun
champ de visibilité séparé sur le catalogue. Ne code PAS de second
mécanisme de filtrage.

### 4. `WorkspaceImageCatalogReconciler` — premier reconciler de WorkspaceImage

`WorkspaceImage` n'a aujourd'hui AUCUN reconciler — c'est le premier.
Fichier suggéré : `operator/internal/controller/workspaceimage_catalog.go`,
modèle à suivre : `namespace_janitor.go` pour la forme d'un
`Reconciler` indépendant + `workspace_controller.go` pour le pattern de
self-requeue (`ctrl.Result{RequeueAfter: interval}`).

Logique par `WorkspaceImage` dont `spec.registry != "" && spec.catalog != nil`
— deux branches **mutuellement exclusives** selon `spec.catalog.from`
(exactement l'une des trois options, garanti par la XValidation du
type, § Décision d'architecture centrale) :

**Branche A — `From.URL` défini (live, périodique) :**
1. `GET` HTTP simple (`net/http`, pas de nouvelle dépendance — PAS
   `crane`/`go-containerregistry`) sur `from.url`, timeout raisonnable
   (5-10s). Si `spec.catalog.auth.bearerToken != nil`, lis le Secret
   nommé par `auth.bearerToken.secretRef` dans le namespace plateforme
   (lecture non cachée, même doctrine que `pull_secret.go` — "Secret
   reads bypass the cache", commentaire RBAC existant
   `helm/waas/templates/operator.yaml:31-34`) et ajoute
   `Authorization: Bearer <clé "token" du Secret>`. Secret
   manquant/illisible/sans clé `token` → échec de sync (`lastSyncError`),
   jamais un crash.
2. Succès + parse OK → parse la réponse dans `catalog.File`/`catalog.Entry`
   (`operator/pkg/catalog`, § 2 — le même type que le générateur de
   schéma, jamais un parsing ad-hoc), puis convertit chaque `catalog.Entry`
   en `DiscoveredImage` pour `status.catalog.entries` (les deux types sont
   volontairement distincts, § 2 — cette conversion est le seul point
   de couture entre eux). `status.catalog.source = "Fetched"`,
   `status.catalog.lastSyncTime = now`, `status.catalog.lastSyncError = ""`.
3. Échec (réseau, HTTP non-200, auth, parse) → **ne jamais vider un
   `status.catalog.entries` déjà peuplé** (stale-but-served) ;
   `status.catalog.lastSyncError` mis à jour, `status.catalog.source`
   inchangé.

**Branche B — `From.ConfigMapKeyRef` ou `From.SecretKeyRef` défini
(statique, GitOps-managed, § 5) :**
1. Lit la clé référencée (`ConfigMapKeyRef.Key`/`SecretKeyRef.Key`,
   défaut `catalog.yaml` seulement pour `ConfigMapKeyRef` — requis pour
   `SecretKeyRef`) dans le namespace plateforme. Lecture non cachée
   pour le cas Secret (même doctrine que `auth.bearerToken.secretRef` ;
   le SA operator a déjà `[get, list, ...]` sur `configmaps` ET
   `secrets` sans restriction de ressource,
   `helm/waas/templates/operator.yaml:33-40` — aucun nouveau RBAC).
   Aucun appel HTTP dans cette branche.
2. Succès + parse OK (même `catalog.File`, § 2, jamais un format
   spécial) → `status.catalog.entries` = le contenu converti,
   `status.catalog.source = "Static"`, `status.catalog.lastSyncTime = now`,
   `status.catalog.lastSyncError = ""`.
3. Échec (objet manquant, clé absente, contenu qui ne parse pas) →
   **ne jamais vider un `status.catalog.entries` déjà peuplé**
   (stale-but-served, même doctrine que la branche A) ;
   `status.catalog.lastSyncError` mis à jour, jamais un crash.

**Commun aux deux branches :**
4. Requeue après `RequeueAfter: interval` (succès ou échec, les deux
   branches confondues — la branche B se re-lit au même rythme que la
   branche A re-fetch, aucun mécanisme de rafraîchissement immédiat
   séparé, voir pourquoi juste en dessous). `interval` vient d'une
   variable d'env opérateur (§ 7).
5. Watch sur `WorkspaceImage` propre à ce reconciler : une édition de
   `spec.catalog` (y compris un changement de branche A→B ou
   inversement) retrigger immédiatement (comportement par défaut d'un
   `For(&WorkspaceImage{})` — pas besoin de predicate supplémentaire,
   ce reconciler filtre lui-même dans `Reconcile` les entrées sans
   `spec.catalog`).

**Pas de watch sur la ConfigMap/le Secret référencé par la branche B**
— ça aurait été la mécanique idiomatique controller-runtime pour un
rafraîchissement immédiat (édition GitOps → reconciliation instantanée
au lieu d'attendre `interval`), mais `helm/waas/templates/operator.yaml:32,36`
documente explicitement que les lectures Secret/ConfigMap de cet
opérateur **contournent le cache** ("Secret reads bypass the cache...
no watch", "Uncached reads too") — choix architectural déjà en place,
RBAC sans le verbe `watch` sur ces deux ressources. Un `Watches(&corev1.ConfigMap{}, ...)`
irait à l'encontre de ce choix (RBAC à étendre, cache à activer pour
ces GVK). La branche B accepte donc le même délai de propagation que
la branche A (jusqu'à `catalogSyncInterval`, § 7) — cohérent plutôt
qu'un cas spécial, et sans rouvrir une décision d'architecture déjà
actée ailleurs dans ce repo.

Ce reconciler ne bloque JAMAIS la création de workspace : séparé,
purement cosmétique, `enforce()`/`FindImage` ne le lisent jamais.

### 5. Source du catalogue : URL fetchée en direct OU contenu statique — jamais un snapshot embarqué dans le binaire

**Écart volontaire par rapport à deux premiers brouillons.** Le tout
premier proposait d'embarquer deux fichiers figés via `//go:embed`
(précédent : `kasmvnc_defaults.yaml`,
`operator/internal/controller/kasm_config.go`), mappés en dur par
`spec.registry` exact (`ghcr.io/xorhub/waas-images` → un fichier,
`docker.io/kasmweb` → un autre) — écarté : ni générique (mapping en dur
limité à deux registres, alors que N'IMPORTE QUELLE `WorkspaceImage` en
mode registre peut porter un `spec.catalog`), ni GitOps-compliant
(rafraîchir le snapshot exige de recompiler et publier une nouvelle
version de l'opérateur), ni aussi auditable pour un admin qu'un objet
K8s inspectable directement (`kubectl get configmap -o yaml` contre
"aller lire le Go source de l'opérateur"). Un second brouillon a ensuite
introduit `ConfigMapKeyRef`/`SecretKeyRef` comme un **fallback**
utilisé UNIQUEMENT quand le fetch live n'avait jamais réussi (`url`
restant un champ séparé, toujours présent) — écarté à son tour :
`url` et `from.{configMapKeyRef,secretKeyRef}` auraient pu coexister en
même temps sur le même objet, avec une sémantique implicite de
priorité (le live gagne s'il a déjà réussi une fois) qu'il fallait
documenter et tester séparément, pour un besoin qui n'existe pas
vraiment — un admin choisit UNE source, pas une source-avec-un-plan-B.

**Solution retenue** : `ImageCatalogSpec.From` (§ Décision
d'architecture centrale) est un struct `ImageCatalogSource` avec
`URL`/`ConfigMapKeyRef`/`SecretKeyRef` **mutuellement exclusifs à
trois** (exactement un des trois, jamais deux, jamais zéro — la
XValidation du type l'impose). Pas de notion de "fallback" ni de
priorité entre les trois : c'est un choix de source, un point, pas une
hiérarchie. `URL` reste le fetch live périodique (§4 branche A). Le cas
`ConfigMapKeyRef` couvre l'admin qui préfère un catalogue statique
GitOps-managed (contenu non secret, cas courant) ; `SecretKeyRef` existe
pour l'admin qui veut restreindre l'accès au contenu du manifeste
lui-même — **sans rapport** avec `ImageCatalogSpec.Auth.BearerToken`
(le credential du fetch live, uniquement pertinent avec `From.URL`,
voir la XValidation qui les couple) : les deux servent des rôles
distincts et peuvent pointer vers des Secrets différents. Générique
(n'importe quelle `WorkspaceImage` en mode registre choisit sa source,
pas seulement les deux registres connus), GitOps-managed pour les deux
variantes statiques (la ConfigMap/le Secret se met à jour par un
commit, comme `gitops/governance/images.yaml`, et le reconciler la
re-lit au prochain `catalogSyncInterval` — pas de watch dédié sur ces
deux ressources, §4 explique pourquoi), et zéro nouveau RBAC (le SA
operator a déjà
`[create, delete, get, list, update]` sur `configmaps` ET `secrets`
sans restriction de ressource, `helm/waas/templates/operator.yaml:33-40`).

Pour les deux registres publics connus (`ghcr.io/xorhub/waas-images`,
`docker.io/kasmweb`), fournis un **exemple** de manifeste ConfigMap
(variante `ConfigMapKeyRef`, le contenu n'étant pas secret) sous
`gitops/governance/examples/` (non appliqué par défaut — le mode
`spec.registry`+`spec.catalog` est déjà lui-même opt-in,
`gitops/governance/images.yaml` n'en utilise aucun aujourd'hui, toutes
ses entrées sont en `spec.image` exact) : un admin qui active le mode
catalogue pour l'un de ces registres choisit lui-même sa source
(`url` vers le registre public, ou copie/adapte l'exemple ConfigMap
pour un usage airgap) plutôt que de dépendre d'un défaut imposé.

### 6. Frontend — exposition API + picker visuel unifié

- `GET /catalog` (`Governance.Catalog`, api-server) : ajoute les
  entrées `WorkspaceImage.status.catalog.entries` des images de
  catalogue autorisées (réutilise `policy.AllowedImages` pour le
  filtrage — même gate que le reste, § 3). **Forme imbriquée** :
  `CatalogImage.discovered?: DiscoveredImage[]` (nouveau champ sur le
  type `CatalogImage` existant, `frontend/src/types.gen.ts:435-464`) —
  pas un tableau séparé au niveau racine de la réponse. Chaque carte du
  picker (§ ci-dessous) a besoin à la fois de l'entrée découverte
  (icône/version/app) ET des bornes/protocoles de la `CatalogImage`
  parente (`protocols`/`min`/`max`/`defaults`, déjà présents sur ce
  type aujourd'hui) — l'imbrication évite toute recorrélation manuelle
  côté frontend entre deux listes.
- Nouveau composant de résolution d'icône,
  `frontend/src/lib/icon.ts` : `resolveIcon(slug?: string, os?: string): string`
  — retourne le chemin d'un asset vendoré localement
  (`/icons/<slug>.svg`), fallback sur `/icons/os-<linux|windows>.svg`
  si `slug` absent ou pas vendoré. **Aucun fetch réseau à l'exécution**.
- **Composant de carte unifié**, utilisé pour la liste des templates
  EXISTANTS et pour les entrées catalogue (pas deux rendus distincts) :
  tout template a au moins un OS connu (`WorkspaceTemplate.spec.os`),
  donc `resolveIcon(undefined, tpl.os)` retourne déjà un logo de
  fallback cohérent pour un template qui n'a pas d'icône catalogue —
  c'est ce qui permet, dès CE volet, de remplacer le `<select>` texte
  actuel de `CreateWorkspaceDialog.tsx` (L267-286) par une grille de
  cartes avec logos, sans attendre le volet B. Réutilisé par
  `SessionCard`/`WorkspaceCard`
  (`frontend/src/components/SessionCard.tsx`,
  `frontend/src/sections/WorkspacesSection.tsx`) pour la même
  cohérence visuelle. Ce même composant sert de brique au picker du
  volet B (qui, lui, ajoute les cartes "depuis le catalogue" à côté des
  cartes template dans la même grille) — garder le composant
  suffisamment générique (icône + titre + sous-titre + état
  disabled/reason) pour que le volet B n'ait qu'à lui fournir des
  données, jamais à le réécrire.
- Vendoring des icônes : `dashboard-icons`
  (github.com/homarr-labs/dashboard-icons, Apache-2.0, structure
  `svg/<slug>.svg`) — copie manuelle/scriptée d'un sous-ensemble
  (slugs référencés par les deux catalogues connus + `linux`/`windows`
  en fallback) dans `frontend/public/icons/`, avec
  `frontend/public/icons/ATTRIBUTION.md` citant la licence Apache-2.0
  et le repo source. `hack/vendor-icons.sh` (sophistication libre —
  liste de slugs en dur suffit, ce n'est pas un chemin d'exécution
  runtime) télécharge depuis
  `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/<slug>.svg`
  pour faciliter les rafraîchissements futurs, mais n'est PAS exécuté à
  l'exécution de l'app.

### 7. Helm — intervalle de sync, RBAC status

`helm/waas/values.yaml` : nouvelle clé `operator.catalogSyncInterval`
(défaut `6h`, même esprit que `apiServer.eventsPollInterval`). Câblée
en variable d'env opérateur.

RBAC (`helm/waas/templates/operator.yaml`) :
- L67-69, `workspacetemplates, workspaceimages, workspacepolicies`
  n'ont que `[get, list, watch]` aujourd'hui. Ajoute une entrée
  séparée `workspaceimages/status` avec `verbs: [get, update, patch]`
  (miroir du bloc `workspaces/status` juste en dessous) — régénère
  avec `make manifests` une fois le marker `+kubebuilder:subresource:status`
  posé, vérifie que le YAML généré correspond.
- `secrets` a déjà `[create, delete, get, list, update]` sans
  restriction de ressource (L33-34) — couvre la lecture de
  `spec.catalog.auth.bearerToken.secretRef` et de
  `spec.catalog.from.secretKeyRef`, aucun ajout nécessaire. De même
  pour `configmaps` (L38-40, mêmes verbes) — couvre
  `spec.catalog.from.configMapKeyRef`. Aucun des deux n'a (ni ne doit
  gagner) le verbe `watch` : ces deux resources contournent
  délibérément le cache de l'opérateur (commentaires existants
  L32/L36) — voir §4 pour pourquoi ce choix reste inchangé ici.

### 8. Régénération

`make manifests generate docs-params generate-types` — CRD YAML
(`helm/waas/crds/waas.xorhub.io_workspaceimages.yaml`), types TS
générés (`frontend/src/types.gen.ts`), et depuis `generate`
(désormais) `operator/pkg/catalog/schema/v1.schema.json` (§ 2).

## Contraintes

- [ADR 0002](../adr/0002-crd-evolution.md) : additif uniquement, aucun
  champ renommé/retypé, `v1alpha1` reste `v1alpha1`.
- `status.catalog` n'est JAMAIS lu par `enforce()`/`FindImage`/`ImageAllowed` —
  cosmétique uniquement.
- Le fetch catalogue ne doit JAMAIS pouvoir bloquer ou retarder la
  création/l'usage d'un workspace — reconciler séparé, pas un appel
  synchrone dans `enforce()` ou `buildPodTemplate`.
- Aucune nouvelle dépendance Go pour le fetch (pas de
  `go-containerregistry`/`crane` — un `GET` HTTP suffit). Ne s'applique
  PAS à la dépendance de génération de schéma (§ 2,
  `invopop/jsonschema` ou équivalent) : outil de `make generate`
  uniquement, jamais importé par `cmd/operator`, donc absent du binaire
  livré et sans rapport avec le chemin de fetch runtime que cette règle
  vise.
- Pas de nouvelle CR `WorkspaceCatalog` — décision actée § Décision
  d'architecture centrale, ne la réintroduis pas sans repasser par une
  revue d'architecture explicite.

## Tests

- `WorkspaceImageCatalogReconciler`, branche A (`From.URL`) : fetch OK
  (avec et sans `auth.bearerToken`), fetch KO avec `entries` déjà
  peuplé (stale-but-served), parse d'un `apiVersion` inconnue (rejet
  propre), auth avec Secret manquant/sans clé `token` (échec de sync,
  pas de crash — ce cas concerne `Auth.BearerToken.SecretRef`, à ne pas
  confondre avec `From.SecretKeyRef` de la branche B dans les fixtures
  de test).
- `WorkspaceImageCatalogReconciler`, branche B (`From.ConfigMapKeyRef`/`SecretKeyRef`) :
  lecture OK (les deux variantes), lecture KO avec `entries` déjà
  peuplé (stale-but-served), ConfigMap/Secret manquant, clé absente,
  contenu malformé (tous fail-soft, jamais un crash). Pas de test de
  rafraîchissement immédiat sur édition externe — délibérément absent
  (§4), la branche B se re-lit au même rythme que la branche A.
- XValidation `ImageCatalogSource` : rejet à l'admission si zéro ou
  plusieurs de `url`/`configMapKeyRef`/`secretKeyRef` sont renseignés ;
  XValidation `ImageCatalogSpec` : rejet si `auth` est défini sans
  `from.url`.
- Schéma catalogue (§ 2) : `make generate` régénère
  `operator/pkg/catalog/schema/v1.schema.json` sans diff — validé par
  le job `go-generated-drift` existant (chemin ajouté à son
  `git diff --exit-code`), pas un nouveau test à écrire.
- Frontend : Vitest sur `resolveIcon` (slug connu, slug inconnu,
  absent, fallback OS) ; le composant de carte unifié rend un template
  SANS icône catalogue avec le fallback OS, et une entrée catalogue
  avec son icône propre.
- `/verify` (skill du repo) sur le parcours complet si possible :
  `WorkspaceImage` avec `spec.catalog.from.url` pointant un fichier de
  test local (`httptest.Server` pour les tests Go), vérifier que le
  picker du portail affiche bien les entrées découvertes avec logo.

## Points ouverts (ton arbitrage)

Aucun à ce stade — le point qui restait ouvert (comment ajouter une
future méthode d'auth catalogue sans renommer `token`) est résolu
structurellement par `ImageCatalogAuth` (§ Décision d'architecture
centrale) : un futur `basicAuth`/`mTLS`/etc. s'ajoute comme un nouveau
champ frère de `bearerToken`, jamais une réinterprétation d'un champ
existant.
