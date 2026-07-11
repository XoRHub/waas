# Prompt Fable 5 — Audit 2 : remédiation des constats « Small » (ordres 1, 2, 4, 5, 6, 7, 8)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Source

`docs/studies/20-report-audit2-organisation-doublons-securite.md` est le
rapport d'audit complet (2026-07-11). Lis-le d'abord en entier,
notamment le tableau des constats §4 et le plan d'action §5 — ce prompt
t'évite de refaire la recherche fichier:ligne pour chaque constat, mais
le §4 donne le contexte et le raisonnement complets derrière chacun.

## Périmètre de ce prompt

Le plan d'action (§5) range les constats en 10 ordres, du quick-win au
chantier structurant. Ce prompt couvre **les ordres 1, 2, 4, 5, 6, 7 et
8** — tous les ordres d'effort « S » sauf un, avec un ordre traité
différemment de ce que dit le rapport :

- **Ordre 3 (C3 activer Renovate + C5 bump trivy) — EXCLU de ce prompt.**
  Activer l'app Renovate est une action GitLab (paramètres projet), pas
  un changement de code ; ne le fais pas dans le cadre de ce prompt.
- **Ordre 5 (C13, secrets Helm via `lookup`) — traité en mode FIX, pas
  vérification.** Le rapport propose « S (vérifier le rendu ArgoCD) → M
  (fix si confirmé) ». **C'est déjà confirmé** : la fonction Helm
  `lookup` ne fonctionne pas sous ArgoCD (le repo-server rend le chart
  sans accès au cluster de destination, `lookup` y retourne toujours une
  map vide) — ne perds pas de temps à re-vérifier ce point, va
  directement à l'implémentation d'un fix propre (détails ci-dessous,
  section Ordre 5).
- **Ordres 9, 10 et la dernière ligne du plan (« au fil de l'eau ») sont
  hors périmètre** — ce sont les chantiers M/discutables, laissés pour
  un prompt dédié plus tard.

Chaque section ci-dessous correspond à un ordre du plan. Traite-les dans
l'ordre (certains touchent les mêmes fichiers CI, voir les notes de
séquencement). Un commit par ordre (ou par constat si tu préfères du
grain plus fin), jamais un unique commit fourre-tout — ce sont des
changements de nature différente (ménage doc, CI, types générés,
sécurité, GitOps).

---

## Ordre 1 — Ménage (C24, C4, C23, C22)

- **C24 (hygiène git des docs)** : au moment de la rédaction de ce
  prompt, `git status` est déjà propre — ce constat semble résolu depuis
  le rapport. Vérifie-le quand même (`git status --short`) ; s'il y a
  des fichiers non trackés/modifiés sous `docs/`, commit-les avant de
  continuer, sinon ne fais rien pour ce point.
- **C23 (vestige racine)** : `fable-waas-build-prompt.md` à la racine du
  repo est le brief de bootstrap historique, sans usage courant. Par
  défaut `git rm` (le rapport le préfère : « une minute »). Si tu juges
  qu'il a une valeur d'archive, déplace-le dans `docs/studies/` au lieu
  de le supprimer — documente ton choix dans le message de commit. tu peux le supprimer de l'arbre complement
- **C22 (spec morte)** : `docs/openapi-governance.yaml` est une spec
  OpenAPI manuelle, partielle, non consommée (grep le repo pour
  confirmer qu'aucun fichier ne la référence hors de `docs/`) et
  redondante depuis que tygo génère les types. Supprime-la.
- **C4 (commentaires CI menteurs sur eslint)** — **ne le fais PAS en
  isolation ici** : traite-le dans l'Ordre 2 ci-dessous, en même temps
  que l'ajout du job eslint GitLab. Si tu corriges le commentaire avant
  que le job existe réellement côté GitLab, il redevient faux entre les
  deux commits. Note-le comme reporté à l'Ordre 2, ne le commit pas deux
  fois.

---

## Ordre 2 — CI : rendre à GitLab les gates déjà écrits (C2, C14) + C4

`.gitlab/ci/go.yml` et `.gitlab/ci/frontend.yml` sont la CI qui compte
réellement (le remote local est GitLab seul, `docs/ci.md:1` : « Un seul
point d'entrée : `.gitlab-ci.yml` »). Trois gates existent déjà côté
GitHub (`.github/workflows/ci.yml`) mais pas côté GitLab :

### 2.1 — envtest sur GitLab (`go-test`, module `operator`)

`.gitlab/ci/go.yml:30-44` (job `go-test`, matrice
`MODULE: [shared, operator, api-server, wwt]`) ne positionne jamais
`KUBEBUILDER_ASSETS` : la suite `operator/test/envtest/` skippe
silencieusement (`suite_test.go:12-13` — vérifie la ligne exacte, le
rapport la situe là). Le mécanisme GitHub à répliquer
(`.github/workflows/ci.yml:242-245`) :

```yaml
- name: Install envtest control plane
  if: matrix.module == 'operator'
  working-directory: operator
  run: echo "KUBEBUILDER_ASSETS=$(make -s envtest-assets)" >> "$GITHUB_ENV"
```

Le target `make envtest-assets` existe déjà (`operator/Makefile:27`).
Adapte au `script:` GitLab (pas de `$GITHUB_ENV`, juste un `export`
avant l'appel à `go test`), conditionné sur `$MODULE = "operator"` :

```yaml
script:
  - cd "$MODULE"
  - |
    if [ "$MODULE" = "operator" ]; then
      export KUBEBUILDER_ASSETS="$(make -s envtest-assets)"
    fi
  - go test -race -covermode=atomic -coverprofile=coverage.out ./...
  - go tool cover -func=coverage.out | tail -n 1
```

### 2.2 — job eslint sur GitLab (`.gitlab/ci/frontend.yml`)

Le fichier n'a que `frontend-typecheck` (tsc -b) et `frontend-test`
(vitest). GitHub a un `npm run lint` (`.github/workflows/ci.yml:298`,
job `frontend`) qui n'existe nulle part côté GitLab. Ajoute un job
`frontend-lint` (stage `lint`, `extends: [.frontend-base,
.rules-frontend]`, `script: [npm run lint]`) — même script que GitHub,
ne réplique pas `format:check` (pas identifié comme un gap par
l'audit, garde le scope de ce constat serré).

### 2.3 — corrige les 2 commentaires menteurs (C4), maintenant qu'ils sont vrais

- `.gitlab/ci/frontend.yml:2` : `# There is no eslint config in the
repo (yet): tsc -b is the lint gate.` → remplace par un commentaire
  correct maintenant que le job `frontend-lint` existe (ex. « TypeScript
  build check + eslint + vitest. »).
- `.github/workflows/ci.yml:281` : commentaire miroir (« Frontend gates
  (tsc -b is the lint gate — no eslint config yet). ») — même
  correction côté GitHub, il est tout aussi faux depuis que
  `npm run lint` tourne à la ligne 298.

### 2.4 — ratchet couverture sur GitLab (`go-test`, module `api-server`) — ATTENTION, piège non listé explicitement dans le rapport

Le rapport dit « un appel ratchet » comme s'il suffisait d'ajouter la
ligne `hack/ci/coverage-ratchet.sh …`. **Ça ne suffit PAS tel quel** :
`hack/ci/coverage-ratchet.sh:1-9` attend un profil produit avec
`-coverpkg=./...` (le commentaire en tête du script est explicite : «
Reads a merged -coverpkg profile, where every test binary emits blocks
for EVERY package »). Or le job GitLab actuel
(`.gitlab/ci/go.yml:42`) lance `go test -race -covermode=atomic
-coverprofile=coverage.out ./...` **sans** `-coverpkg` — le chiffre
mesuré serait alors la couverture locale basse (2-31 %, l'ancien
chiffre de juillet), pas le chiffre cross-package (55,3 %/77,7 %) sur
lequel les seuils `internal/handler:40`/`internal/repository:50` sont
calibrés. Ajouter le ratchet sans `-coverpkg` ferait échouer la CI
GitLab immédiatement sur un faux négatif.

Réplique donc, pour `MODULE == "api-server"` uniquement, la même
mécanique que GitHub (`.github/workflows/ci.yml:230-263`) :

- Un service Postgres (`postgres:` image, healthcheck) pour le test
  dual-backend (`WAAS_TEST_PG_URL`, voir
  `api-server/internal/repository/backends_test.go`).
- `-coverpkg=./...` au lieu de `-coverpkg` absent, uniquement pour ce
  module (garde `./...` simple pour les 3 autres modules — inutile et
  potentiellement plus lent pour eux).
- L'appel ratchet après : `hack/ci/coverage-ratchet.sh
api-server/coverage.out internal/handler:40 internal/repository:50`.

GitLab CI n'a pas de `services:` par défaut comme GitHub Actions au
niveau job matriciel simple — vérifie la syntaxe `services:` GitLab CI
(image `postgres:16-alpine` ou équivalent, variables
`POSTGRES_PASSWORD`/`POSTGRES_DB`) et adapte `WAAS_TEST_PG_URL` pour
pointer le hostname du service (`postgres` par défaut en GitLab CI, pas
`localhost` comme sur le runner GitHub).

### 2.5 — coverage.all frontend (C14), sur les DEUX CI

`.gitlab/ci/frontend.yml:31-33` et
`.github/workflows/ci.yml:301-306` lancent tous les deux `npx vitest
run --coverage.enabled --coverage.provider=v8 …` **sans**
`--coverage.all` : la couverture ne compte que les fichiers déjà
importés par un test (62,4 %, chiffre gonflé) au lieu du vrai total
« tout `src/` » (40,9 % mesuré dans le rapport). Ajoute aux deux
commandes :

```
--coverage.all --coverage.include='src/**/*.{ts,tsx}'
```

plus les exclusions nécessaires pour ne pas compter les tests eux-mêmes
et les fichiers générés/déclaratifs : `--coverage.exclude` pour
`src/**/*.test.{ts,tsx}`, `src/types*.ts`, `**/*.d.ts` (vérifie la
syntaxe exacte d'exclusion vitest — plusieurs flags `--coverage.exclude`
répétés ou une liste, selon la version de vitest verrouillée dans
`package.json`).

---

## Ordre 4 — D2/D3 résiduels : types dupliqués à la main (C7, C8)

### 4.1 — C7 : `RetainedVolume` et `RemoteWorkspaceAdmin` recopiés à la main

`frontend/src/types.ts` définit `interface RetainedVolume` (ligne 123)
et `interface RemoteWorkspaceAdmin` (ligne 265) **à la main**, alors que
`frontend/src/types.gen.ts` génère déjà des types identiques (lignes
271 et 295) depuis `api-server/internal/model` via tygo. Le fichier a
déjà exactement le pattern à suivre pour tout le reste du fichier
(lignes 15-35) : import depuis `./types.gen`, puis réexport via
`export type { … }`. Applique ce même pattern :

1. Supprime les deux `interface` manuelles (lignes 123-131 et 265-278
   au moment de la rédaction de ce prompt — vérifie les lignes exactes,
   elles ont pu bouger).
2. Ajoute `RetainedVolume` et `RemoteWorkspaceAdmin` à l'import
   `from './types.gen'` (lignes 15-22) et au bloc `export type { … }`
   (lignes 23-35).
3. `cd frontend && npm run typecheck` pour confirmer qu'aucun
   consommateur ne cassait sur un léger écart de forme entre la copie
   manuelle et la version générée (il ne devrait pas y en avoir, mais
   vérifie).

Le rapport demande aussi de « balayer le reste des 362 lignes de
types.ts pour tout type présent dans model.go » — fais cette passe :
pour chaque `interface`/`type` restant dans `types.ts` qui n'est pas
déjà réexporté depuis `types.gen.ts` ou `types.manual.ts`, vérifie s'il
existe une définition générée équivalente dans `types.gen.ts` (même
nom ou structure identique) et migre-le de la même façon si c'est le
cas. Ne migre PAS un type qui n'a pas d'équivalent généré (ex. types
purement frontend comme `Theme`, `WorkspaceConnectionPrefs`) — ce
n'est pas de la duplication, laisse-les en place.

### 4.2 — C8 : garde de synchro pour `WorkspacePhase`

`frontend/src/types.ts:82-83` recopie à la main l'union littérale des 7
phases (`'Pending' | 'Provisioning' | … | 'Terminating'`). La source de
vérité est `operator/api/v1alpha1/workspace_types.go:9-25` (marker
kubebuilder `+kubebuilder:validation:Enum=Pending;Provisioning;…` sur le
type `WorkspacePhase`). tygo génère ce champ en `phase: string` (type
trop large pour porter l'union, `types.gen.ts:198`) — le drift-check CI
ne couvre donc pas une désynchronisation des phases.

Le repo a déjà exactement le modèle à répliquer :
`operator/pkg/params/protocols_sync_test.go` compare un enum lu dans un
CRD généré (`operator/config/crd/bases/*.yaml`) à une liste de
référence Go, avec un helper qui repère l'enum recherché par ses
valeurs stables (`hasVNC`/`hasRDP`) plutôt que par chemin JSON figé.
Le même enum de phases existe déjà, généré, dans
`operator/config/crd/bases/waas.xorhub.io_workspaces.yaml` (repère-le
par une valeur stable comme `Pending`+`Running`, même logique que
`findProtocolEnums`).

Écris un test Go (à côté de `protocols_sync_test.go`, ou dans un nouveau
fichier `operator/pkg/params/phase_sync_test.go` si le package
`params` n'a pas de dépendance naturelle sur les phases — à toi de
choisir l'emplacement le plus cohérent) qui :

1. Lit l'enum de phases dans `waas.xorhub.io_workspaces.yaml`.
2. Lit l'union littérale `WorkspacePhase` dans
   `frontend/src/types.ts` par une regex simple sur la déclaration
   `export type WorkspacePhase =\n  'A' | 'B' | …;` — pas besoin d'un
   vrai parseur TS, un extract regex suffit comme le fait déjà
   `findProtocolEnums` côté YAML.
3. Compare les deux listes triées, échoue avec un message explicite si
   elles divergent (même style que le message d'erreur de
   `protocols_sync_test.go`).

Attention au chemin relatif vers `frontend/src/types.ts` depuis le
module `operator/` (`operator/go.mod` est un module Go séparé du reste
du repo, mais rien n't'empêche de lire un fichier texte hors module via
un chemin relatif classique) — vérifie le chemin en lançant le test
localement plutôt que de le déduire.

---

## Ordre 5 — Secrets Helm : abandon de `lookup`, fix propre pour ArgoCD (C13)

### Contexte, déjà tranché — ne pas re-vérifier

`helm/waas/templates/secrets.yaml:1-4` génère `postgres-password`,
`internal-token` et `jwt-private-key` via `lookup "v1" "Secret" …` pour
ne pas les régénérer à chaque `helm upgrade` (ré-utilise la valeur déjà
en cluster si elle existe, sinon en génère une nouvelle aléatoire).
`docs/ci.md:5-6` confirme qu'ArgoCD est bien le consommateur réel de ce
chart (« ArgoCD continue de déployer depuis le tag Git (`path:
helm/waas`) », `docs/ci.md:62`) — pas juste une possibilité
hypothétique.

**C'est confirmé : `lookup` ne fonctionne pas sous ArgoCD.** Le
repo-server d'ArgoCD rend les charts Helm via un `helm template`
côté serveur, sans accès au cluster de destination — `lookup` y
retourne systématiquement une map vide, que la ressource existe ou non
dans le cluster cible. Conséquence concrète si rien ne change : **à
chaque sync ArgoCD**, les 3 secrets seraient régénérés avec des valeurs
aléatoires fraîches — mot de passe Postgres divergent de celui déjà
écrit sur le PVC (connexion DB cassée), `internal-token` changé (appels
wwt→api-server rejetés), `jwt-private-key` changée (toutes les sessions
utilisateur invalidées instantanément). C'est un incident de prod
garanti au premier sync, pas un risque théorique.

**N'utilise `lookup` nulle part dans ce fix.** Ne te contente pas non
plus de vérifier/documenter le problème — implémente la correction.

### Ce qui est attendu

Un mécanisme qui garantit, **identiquement que le chart soit installé
via `helm install`/`helm upgrade` en direct (dev) ou via ArgoCD (prod,
rendu hors-cluster)** :

- Au premier déploiement : les 3 valeurs sont générées aléatoirement et
  persistées dans le Secret `{{ .Release.Name }}-secrets`.
- À chaque déploiement suivant (upgrade / sync) : les 3 valeurs
  **existantes ne sont jamais régénérées ni écrasées** si le Secret
  existe déjà avec ces clés.
- `database-url` (actuellement calculé dans le même template à partir
  de `$pgPassword`, ligne 19) doit continuer à refléter le mot de passe
  Postgres réellement actif (qu'il vienne de `.Values.postgres.password`
  ou de la valeur générée une fois).
- `admin-password` (ligne 23-25, dérivé directement de
  `.Values.postgres.adminPassword`... — relis la ligne exacte, c'est
  `.Values.apiServer.adminPassword`) reste un simple passage de Values,
  aucun changement requis dessus, il n'est pas généré.
- Aucune nouvelle dépendance externe (pas d'External Secrets Operator,
  pas de Sealed Secrets) — le rapport les cite comme alternative
  possible, mais ajouter un opérateur supplémentaire pour ce seul
  problème est disproportionné ; le fix doit rester dans le chart
  lui-même.

**Piste recommandée** (tu peux en choisir une autre si elle respecte
mieux les contraintes ci-dessus, documente ton choix) : un **Job Helm
hook** (`helm.sh/hook: pre-install,pre-upgrade`,
`helm.sh/hook-delete-policy: before-hook-creation`, poids assez bas
pour s'exécuter avant les Deployments qui consomment le Secret) qui,
lui, tourne **dans le cluster réel** (que le manifeste ait été produit
par `helm template` côté ArgoCD ou appliqué directement par
`helm install`, une fois planifié en Job c'est un Pod qui s'exécute
avec un vrai accès à l'API serveur) et fait, de façon idempotente :
vérifier si le Secret `{{ .Release.Name }}-secrets` existe déjà avec
les bonnes clés ; si non, générer les valeurs manquantes et
créer/patcher le Secret ; si oui, ne rien toucher (sauf
`database-url`/`admin-password` si ces parties restent pilotées par
Values et doivent donc être tenues à jour même quand le Secret
existe déjà — à toi d'arbitrer si un patch partiel est nécessaire ou si
tout doit être Job-managed pour rester cohérent).

Ce Job a besoin d'un ServiceAccount + Role scopés au namespace de
release, avec seulement `get`/`create`/`update` sur `secrets`, restreint
si possible au nom exact `{{ .Release.Name }}-secrets` (pas de
`create`/`update` large sur tous les secrets du namespace).

### Contraintes de vérification

- `helm template` seul (sans cluster, exactement ce que fait le
  repo-server ArgoCD) ne doit fabriquer AUCUNE valeur secrète — le
  chart rendu ne doit contenir ni mot de passe ni token en clair
  produits par un appel `lookup` ou une génération template-time.
- Simule un `helm upgrade` répété (`helm install` puis `helm upgrade`
  deux fois de suite sur un cluster de test, ex. k3d local) et confirme
  que `postgres-password`/`internal-token`/`jwt-private-key` sont
  identiques entre le premier install et le deuxième upgrade (le Job ne
  doit pas les avoir régénérés).
- `helm lint` et `helm template` doivent rester verts (déjà gatés en CI,
  `helm-render` — vérifie que ton nouveau template Job passe ce lint).

### Points ouverts (ton arbitrage)

- Image du Job : le repo n'a aucun précédent de Job/hook Helm existant
  dans `helm/waas/templates/` à répliquer. Choisis une image minimale
  avec accès à l'API Kubernetes (`bitnami/kubectl`, `rancher/kubectl`,
  ou toute image déjà utilisée ailleurs dans ce repo si elle convient)
  — documente ton choix, en particulier si tu dois l'ajouter au
  catalogue Trivy/scan de la CI. utilise bitnami/kubectl
- Si `database-url`/`admin-password` doivent être gérés par le même Job
  ou rester des clés templatées à part dans le même Secret — les deux
  marchent tant que le Secret final a bien toutes les clés attendues
  par `api-server`/`wwt` (vérifie les consommateurs, ex.
  `helm/waas/templates/api-server.yaml`, `postgres.yaml`, pour la liste
  exacte des clés référencées par `secretKeyRef`).

---

## Ordre 6 — Sécurité/repro à faible coût (C11, C12, C6)

### 6.1 — C11 : rate-limit sur le login local

`api-server/internal/service/auth_service.go:37` (`Login`) vérifie
argon2id et audite les échecs, mais rien ne freine un brute-force en
ligne — pas de limite par IP ni par compte. Le routeur
(`api-server/internal/server/router.go:34-58`) utilise déjà
`go-chi/v5` avec `chimiddleware.RealIP` monté globalement (ligne 36),
donc `r.RemoteAddr` est déjà fiable pour un rate-limit par IP même
derrière un proxy.

Ajoute un rate-limit scoped **uniquement à la route
`POST /api/v1/auth/login`** (`router.go:58`) — pas un middleware
global, les autres routes n'ont pas ce problème. Le rapport suggère
`httprate` : `github.com/go-chi/httprate` est l'écosystème naturel vu
que le routeur est déjà `go-chi`, et évite de réinventer un limiteur à
la main. Une limite raisonnable par IP (ex. 10 tentatives/minute) suffit
pour ce constat ; combiner IP+username (comme suggéré par le rapport)
est possible mais demande de lire le body JSON avant qu'il n'atteigne
le handler (donc de le bufferiser pour ne pas le consommer deux fois) —
si tu le fais, documente comment tu évites la double lecture du body,
sinon une limite par IP seule est un fix suffisant pour ce constat.

### 6.2 — C12 : oracle de timing sur l'énumération de comptes

Même fichier, `auth_service.go:38-44` : un username qui n'existe pas
retourne immédiatement `apierror.Unauthorized` (ligne 41) sans jamais
passer par `VerifyPassword`/argon2id, alors qu'un username existant
prend ~50ms (le coût de l'argon2id). L'écart est mesurable à distance.

Fix : sur la branche `errors.Is(err, repository.ErrUserNotFound)`
(ligne 40), appelle quand même `VerifyPassword(password, dummyHash)`
avec un hash PHC argon2id constant (précalculé une fois, pas besoin
d'être secret — juste au bon format pour que le coût de calcul soit
comparable) avant de retourner l'erreur, pour que les deux chemins
prennent un temps similaire. Ignore le résultat du `VerifyPassword`
(il sera toujours `false` puisque c'est un hash bidon), seul le temps
écoulé compte.

**Fais-le dans la foulée** (le rapport le note explicitement) : ce
chemin `ErrUserNotFound` n'émet **aucun événement d'audit**,
contrairement aux 3 autres chemins d'échec du login (SSO-only,
mauvais mot de passe, compte inactif — tous les trois appellent
`s.audit.Record(…, "user.login_failed", …)`, voir lignes 47-48 et 55-56
au moment de la rédaction). Ajoute le même appel d'audit sur ce chemin
pour la cohérence — utilise un identifiant de cible vide/zéro puisqu'il
n'y a pas de `user.ID` pour un compte inexistant (regarde comment
`Actor{Username: username, ClientIP: clientIP}` est déjà utilisé sur
les 3 autres chemins pour rester cohérent sur la forme de l'appel).

### 6.3 — C6 : pins toolchain dev vs CI

`.mise.toml` (racine) :

```
[tools]
go = "1.26"
node = "lts"
k3d = "latest"
```

alors que la CI fige `golang:1.26.4` (`.gitlab/ci/go.yml:33`,
`.github/workflows/ci.yml`) et `node:22.17.0-alpine`
(`.gitlab/ci/frontend.yml:5`). Pin les deux versions exactement sur les
mêmes valeurs que la CI (`go = "1.26.4"`, `node = "22.17.0"`).

`golangci-lint` n'est pas dans `.mise.toml` du tout, alors que la CI
l'utilise en image dédiée (`golangci/golangci-lint:v2.12.2`,
`.gitlab/ci/go.yml:21`) — ajoute `golangci-lint = "2.12.2"` (vérifie
que mise a bien un plugin/registry pour cet outil, sinon documente
l'alternative, ex. un script `mise` `run` ou une note dans le Makefile).

`k3d = "latest"` : pin sur une version exacte (vérifie la version
actuellement installée/utilisée en dev via `k3d version`, ou choisis la
dernière version stable connue au moment de l'exécution — documente ton
choix).

`hack/dev/k3d-config.yaml:12-20` (`kind: Simple`, `servers: 1`) ne
pinne pas l'image k3s (pas de champ `image:` au niveau racine du
manifest) — ajoute `image: rancher/k3s:vX.Y.Z-k3s1` avec une version
cohérente avec le `k3d` pinné ci-dessus (une version de k3d installe
une version de k3s par défaut si non spécifiée ; choisis une version
k3s explicitement compatible et documente le choix, comme le fichier le
fait déjà pour d'autres décisions — voir le commentaire existant
lignes 1-9 sur le mapping de ports).

---

## Ordre 7 — Couverture des composants à fort rayon de souffle (C19, C20)

### 7.1 — C19 : `shared/auth` stagnant (69,4 %), `wwt/internal/jwks` à 0 %

`shared/auth/keys_test.go` (76 lignes) ne couvre que le happy path +
mauvaise audience + expiration + PEM roundtrip
(`TestSignAndVerifyRoundTrip`, `TestVerifyRejectsWrongAudience`,
`TestVerifyRejectsExpired`, `TestPEMRoundTrip`). Chemins d'erreur non
couverts dans `shared/auth/keys.go` : mauvaise méthode de signature
(alg confusion, ex. token signé en `none`/HS256 rejeté par
`jwt.WithValidMethods`), mauvaise clé (signature invalide), `kid`
absent/inconnu, `ParseSignerPEM` avec un PEM invalide ou une clé
non-RSA (ligne 41-58), issuer incorrect. Ajoute des tests
table-driven pour ces chemins, dans le style déjà présent dans
`keys_test.go`.

`wwt/internal/jwks/jwks.go` n'a **aucun test** (0 % de couverture,
73 lignes). Écris `wwt/internal/jwks/jwks_test.go` avec un
`httptest.Server` servant un document JWKS JSON valide, couvrant :

- Fetch initial réussi + `Key()` retourne la bonne clé pour un `kid`
  connu.
- Cache : un deuxième appel à `Key()` dans la fenêtre `cacheTTL` (5 min,
  ligne 28) ne refait pas de requête HTTP (compte les appels reçus par
  le serveur de test).
- Rotation : `kid` inconnu déclenche un `refreshLocked` (ligne 46).
- Erreur réseau/HTTP non-200 sur le refresh : si une clé est déjà en
  cache, `Key()` sert la valeur stale plutôt que de propager l'erreur
  (ligne 47-50, comportement volontaire — teste-le explicitement,
  c'est le genre de choix qui se casse silencieusement en refactor).
- `kid` totalement absent du document JWKS et cache vide : erreur
  propagée (ligne 54-56).

### 7.2 — C20 : `operator/pkg/policy` en régression (84 % → 78,7 %) + ratchet

C'est la seule couverture en baisse du repo : le code
quota/remote-workspaces ajouté depuis juillet est arrivé moins testé
que l'existant dans `operator/pkg/policy/` (`overrides.go`,
`policy.go`). Lance `go test -coverprofile=coverage.out ./pkg/policy/...`
puis `go tool cover -html=coverage.out` (ou `-func=`) pour repérer
précisément les fonctions/branches non couvertes ajoutées récemment
(comparé à `git log -p` sur ce package depuis le 8 juillet si besoin de
contexte sur ce qui est nouveau), et ajoute les tests manquants pour
remonter au-dessus de 84 %.

Une fois remonté, câble ce package dans le mécanisme de ratchet pour
que la régression ne puisse plus se reproduire silencieusement — même
schéma que le ratchet api-server ajouté à l'Ordre 2.4, mais pour le
module `operator` (`MODULE == "operator"` dans `go-test`, les deux CI) :
`hack/ci/coverage-ratchet.sh operator/coverage.out pkg/policy:84`.
Le profil `operator/coverage.out` généré par `go test
-covermode=atomic -coverprofile=coverage.out ./...` (déjà en place,
`.gitlab/ci/go.yml:42`) est-il compatible avec le format attendu par le
script (`-coverpkg=./...` requis, cf. Ordre 2.4) ? Vérifie : si les
tests de `pkg/policy` sont des tests internes au package (pas des tests
d'intégration cross-package comme pour api-server), un profil simple
`-coverprofile` **sans** `-coverpkg` peut suffire à condition que le
format de sortie de `go test` reste compatible avec le parsing awk du
script (une ligne par bloc, `fichier:ligne.col,ligne.col nstmts hits`)
— teste le script en local sur le profil réel avant de considérer le
job fini, ne suppose pas que ça marche sans essayer.

---

---

## Contraintes générales

- N'affaiblis jamais un gate existant (seuils Trivy, ratchets de
  couverture, gates de sécurité) pour faire passer la CI plus
  facilement.
- Chaque ordre ci-dessus est indépendant des autres sauf mention
  contraire (2.3 dépend de 2.2 ; 2.4 et 7.2 touchent le même job
  `go-test` mais des modules différents — vérifie qu'ils ne se
  marchent pas dessus si tu les traites dans le même commit).
- Les numéros de ligne cités datent de la rédaction de ce prompt
  (2026-07-12) — vérifie-les avant d'éditer, ils ont pu bouger de
  quelques lignes depuis.
- Build/tests verts avant de considérer un ordre terminé :
  `go build ./... && go test ./...` par module Go touché,
  `cd frontend && npm run typecheck && npm test` si le frontend est
  touché, `helm lint`/`helm template` si `helm/waas/` est touché.

## Points ouverts (ton arbitrage, au-delà de ceux déjà notés par ordre)

- Granularité des commits (un par ordre vs un par constat) — les deux
  sont acceptables, documente ton choix dans les messages de commit. arbitrage => par ordre
- Si un constat s'avère déjà résolu au moment de l'exécution (comme
  C24 semble déjà l'être), note-le et passe au suivant sans chercher à
  produire un changement artificiel.
