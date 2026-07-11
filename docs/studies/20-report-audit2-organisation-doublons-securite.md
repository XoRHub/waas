# Audit 2 — organisation, doublons, sécurité, tests (2026-07-11)

Second audit complet du repo, en revalidation de
`docs/studies/audit-2026-07.md` (2026-07-08) : chaque constat de
l'audit précédent a été re-testé contre l'état réel du code, les
chiffres de couverture sont **mesurés** (commandes en §3), et chaque
constat du tableau §4 porte sa complexité et un verdict « vaut le
coup ». Périmètre : tous les composants, y compris `operator/test/`,
`hack/dev/`, la CI elle-même et `docs/`.

**Choix de granularité** (point laissé ouvert par le prompt) : un
constat = un problème actionnable. Les « services fourre-tout » sont
groupés en une ligne (même remède, même arbitrage) ; les divergences
entre les deux CI sont éclatées en lignes séparées (remèdes
indépendants, complexités différentes).

## 0. Volet A — fait (commit `83ff30752b16`)

Les 4 jobs rouges de la pipeline main (`trivy-deps`, `scan-frontend`,
`scan-api-server`, `scan-operator`, pipeline 2668804747) sont remédiés
sans toucher aux seuils :

| Dépendance | Avant → après | CVE fermées | Modules |
|---|---|---|---|
| `golang.org/x/net` | v0.47.0 → v0.57.0 | CVE-2026-25681, -27136, -33814, -39821 (HIGH) | api-server, operator, test/smoke (+ wwt via `go work sync`) |
| `golang.org/x/crypto` | v0.45.0 → v0.54.0 | CVE-2026-39828…39835, -42508, -46595, -46597 (HIGH, x/crypto/ssh) | api-server |
| `github.com/jackc/pgx/v5` | v5.7.6 → v5.10.0 | CVE-2026-33815, -33816 (**CRITICAL**, apparues après la rédaction du prompt) | api-server |
| `github.com/moby/spdystream` | v0.5.0 → v0.5.1 | CVE-2026-35469 (HIGH) | api-server |
| Image de base frontend | `nginx-unprivileged:1.27-alpine` → `1.30-alpine` + `apk upgrade` | 35 CVE OS Alpine (openssl CRITICAL ×2, libpng, libexpat, c-ares…) | frontend/Dockerfile |

Vérifié : `go build`/`go test` verts sur les 5 modules ; `trivy fs`
racine et `trivy image` sur le frontend reconstruit **exit 0** avec les
flags exacts de la CI (`aquasec/trivy:0.63.0`, HIGH/CRITICAL,
`--ignore-unfixed`) ; nginx sert toujours en user 101 (HTTP 200).
`package-lock.json` : 0 CVE (reconfirmé). **Aucune CVE sans fix
restante** → pas de risque accepté à consigner. Aucune MR Renovate
n'existait pour ces bumps — voir C3, c'est un constat en soi.

## 1. État des constats de l'audit du 2026-07-08

### Résolus depuis le 8 juillet (vérifiés dans le code)

| Constat de juillet | Preuve de résolution |
|---|---|
| D1 — protocoles ×4 exemplaires | Le switch de `AdminUpsertImage` est un lookup `params.Protocols()` (`api-server/internal/service/governance_service.go:325`) et `operator/pkg/params/protocols_sync_test.go` garde la synchro avec les enums CRD |
| D2 — types TS à la main | tygo génère `frontend/src/types.gen.ts` depuis `api-server/internal/model` (`Makefile:57`), drift-checké en CI GitHub (`.github/workflows/ci.yml:278`). **Reliquat : voir C7** |
| Pas d'envtest | `operator/test/envtest/` couvre exactement les 3 chemins demandés : `crd_validation_test.go` (CEL), `webhook_admission_test.go`, `finalizer_lifecycle_test.go`. **Mais il ne tourne que sur GitHub CI : voir C2** |
| Doctrine create-only podTemplate implicite | `docs/adr/0001-template-boundary-convergence.md`, référencé dans `workload.go:245` |
| Pas de stratégie d'évolution CRD | `docs/adr/0002-crd-evolution.md` |
| `http.Server` sans timeouts | `api-server/cmd/api-server/main.go:167-181` : ReadTimeout 30 s, IdleTimeout 120 s, WriteTimeout 0 commenté (SSE) — exactement ce que demandait l'audit |
| Repository dual-backend à 15,5 % | 69,3 % local, **77,7 %** cross-package (mesuré) ; `docs/testing.md` documente le tier PostgreSQL |
| Handlers à 2,2 % | 55,3 % cross-package : exercés via le vrai router (`internal/server/*_test.go`) + ratchet `hack/ci/coverage-ratchet.sh` (handler ≥ 40, repository ≥ 50) — **ratchet GitHub-only, voir C2** |
| Frontend 7,9 %, aucun eslint | 40,9 % all-files mesuré (143 tests, 26 fichiers) ; `frontend/eslint.config.js` + script `lint` + job GitHub. **eslint absent de la CI GitLab : voir C2 ; chiffre CI trompeur : voir C14** |
| Sleep flaky `event_hub_test.go:77` | Réécrit : la synchro s'établit « WITHOUT a time.Sleep race » (`event_hub_test.go:38`). Les `time.Sleep` restants (wwt, smoke, envtest) sont des boucles de poll bornées, pas des courses |
| N+1 `usageOf` documenté non corrigé | Corrigé : un seul LIST templates + `policy.OwnerLoads` (`governance_service.go:224-228`), le commentaire trace même l'ancienne divergence |
| Clipboard kasmvnc non gouverné | Feature 11 (DLP dérivé de la policy, `/api/downloads` bloqué au proxy `wwt/internal/kasm/kasm.go:145`) + fixes 13/14 vérifiés e2e |
| PortalPage 1617 lignes | Découpée : 100 lignes + `sections/` + dialogs. Le poids s'est déplacé sur TemplatesPage (881 l., voir C17) |

### Toujours ouverts (repris au tableau §4)

D4 (secret-copy ×2), D5 (`envOr` ×2, const CSS — **passée de 4 à 6
copies**), refs `registry.xorhub.io`, pin toolchain dev vs CI,
`guacamole-common-js ^1.5` vs guacd 1.6.0, a11y, OIDC jamais éprouvé
contre un IdP réel (le stub IdP de `internal/server` teste le flux,
pas un vrai Keycloak/Entra).

### Inversés ou périmés depuis juillet

- **« GitHub Actions est le CI canonique, GitLab en retrait planifié »**
  (audit §intro) : la réalité observable est l'inverse — le remote git
  local est GitLab seul, la pipeline qui tourne (et release : cosign,
  chart OCI, smoke) est GitLab, et `docs/ci.md` dit « un seul point
  d'entrée : `.gitlab-ci.yml` » pendant que `docs/ci-github.md:3` dit
  « GitHub est le dépôt canonique ». Voir C1.
- **`cliff.toml` vestige de transition** : faux tant que GitLab vit —
  il est consommé par `release-notes` (`.gitlab/ci/release.yml`,
  git-cliff). À retirer seulement si C1 tranche contre GitLab.
- **`waas-images/` dans le monorepo** : splitté le 2026-07-10 et
  **poussé** (`gitlab.com/drummyjohn/waas-images`, actif le 11/07) ;
  `smoke-connections` le clone (`.gitlab-ci.yml:80`) — dépendance OK.

## 2. Cartographie des nouveautés depuis le 8 juillet

Nouvelles surfaces auditées ici pour la première fois :
`api-server/internal/service/remote_workspace_service.go` (600 l.,
machines hors-cluster via guacd, policy-gated fail-closed, credentials
en Secret jamais en DB — design sain, testé),
`operator/internal/kubevirt/` (40 l., détection KubeVirt pour les VM
Windows, trivial), OIDC-only login (fail-closed au Load, garde 404
handler `auth_handler.go:38-41`), HTTPS dev self-signed
(`hack/dev/k3d-config.yaml` 8443, justifié par le contexte sécurisé du
clipboard), fleet groupé par owner, YamlEditor unifié, i18n fr/en.

## 3. Couverture mesurée (2026-07-11)

Commandes : `go test -cover ./...` par module ;
`go test -coverpkg=./... ./...` + `hack/ci/coverage-ratchet.sh` pour le
cross-package api-server ; vitest
`--coverage.all --coverage.include='src/**/*.{ts,tsx}'` (hors tests,
`types*.ts`, `.d.ts`) pour le vrai chiffre frontend.

| Zone | 08/07 | 11/07 | Lecture |
|---|---|---|---|
| operator `internal/controller` | 70,6 % | 72,7 % | stable |
| operator `internal/webhook` | 85,2 % | 86,5 % | stable |
| operator `pkg/policy` | 84 % | **78,7 %** | seule baisse du repo — le code remote-workspaces/quota ajouté est moins couvert que l'existant |
| operator `pkg/params` / `naming` / `schedule` / `kasmcfg` | 94 / 97,3 / 90,1 / — | 97,0 / 97,3 / 90,1 / 90,5 | bon |
| operator `test/envtest` | n'existait pas | suite dédiée | **skippe sans `KUBEBUILDER_ASSETS`** (C2) |
| api-server `handler` (cross-pkg) | 2,2 % | **55,3 %** | via tests du router + ratchet ≥ 40 |
| api-server `repository` (cross-pkg) | 15,5 % | **77,7 %** | ratchet ≥ 50 |
| api-server `service` / `middleware` (cross-pkg) | 31,9 / — | 65,3 / 76,6 % | bon |
| wwt `guac` / `kasm` / `proxy` | 87 / 86,8 / 62,2 % | 87,4 / 88,1 / 65,0 % | stable ; `cmd` 7,3 % mais `main_test.go:71` exerce désormais le chemin TLS (constat de juillet résolu) |
| shared `auth` | 69,4 % | 69,4 % | stagnant (C19) |
| **frontend (tout src/)** | **7,9 %** | **40,9 %** (926/2265 stmts) | la CI affiche 62,4 % — chiffre naïf, voir C14 |
| e2e | smoke 4 protocoles | + sous-test `vnc-audio`, zéro-orphelin | `test/smoke/smoke_test.go:84` |

Zéros frontend notables (stmts) : `DesktopPane.tsx` 171,
`GovernancePage.tsx` 124, `SplitViewPage.tsx` 83, `ProfilePage.tsx` 73,
`UsersPage.tsx` 48 ; `TemplatesPage.tsx` 31,4 % pour 881 lignes.

## 4. Constats

Catégories : doublon / organisation / sécurité / test / CI-dev-env.
Complexité : S < 1 j, M 1-3 j, L 3-5 j, XL > 5 j (grossier).

| # | Constat | Composant | Source (fichier:ligne) | Catégorie | Gravité/risque | Complexité | Vaut le coup ? |
|---|---|---|---|---|---|---|---|
| C1 | Deux CI complètes maintenues en parallèle avec doctrines contradictoires : `docs/ci.md` (« un seul point d'entrée : .gitlab-ci.yml ») vs `docs/ci-github.md:3` (« GitHub est le dépôt canonique ») ; le remote local est GitLab seul, la release (cosign, chart OCI, smoke bloquant) est GitLab | CI | `docs/ci.md:1`, `docs/ci-github.md:3`, `.gitlab-ci.yml`, `.github/workflows/ci.yml` | CI-dev-env | élevée — chaque gate ajouté doit l'être deux fois et l'oubli est silencieux (C2 en est la preuve ×3) | M (trancher, porter les gates manquants, réécrire les 2 docs) | **Oui** : c'est la racine de C2 ; tant que ce n'est pas tranché, la CI « qui compte » n'exécute pas les meilleurs gates du repo |
| C2 | Trois gates ne tournent QUE sur GitHub alors que la CI vivante est GitLab : envtest (`go.yml:40-44` n'installe pas `KUBEBUILDER_ASSETS` → la suite `operator/test/envtest` skippe silencieusement, cf. `suite_test.go:12-13`), eslint (aucun job dans `.gitlab/ci/frontend.yml`), ratchet couverture (`ci.yml:263` GitHub-only) | CI | `.gitlab/ci/go.yml:40`, `.gitlab/ci/frontend.yml`, `.github/workflows/ci.yml:242,263,298` | CI-dev-env | élevée — l'envtest a été écrit précisément parce que le fake-client a déjà masqué un bug réel, et il ne s'exécute pas sur la CI utilisée | S (3 ajouts YAML indépendants : `make envtest-assets` avant go-test, un job eslint, un appel ratchet) | **Oui** : quick win qui rend à la CI réelle les gates déjà payés ; faisable sans attendre l'arbitrage C1 |
| C3 | Renovate est inerte : `renovate.json` (soigné, 8 custom managers) mais **zéro MR jamais ouverte** sur le projet GitLab — les CVE x/net avec fix disponible depuis 0.53.0 se sont accumulées jusqu'à mettre main en rouge (Volet A) | CI | `renovate.json:1`, `glab mr list --all` vide | CI-dev-env / sécurité | élevée — sans bot, le gate trivy bloquant devient le SEUL signal de CVE, et il bloque main au lieu de proposer une MR en amont | S (activer l'app Renovate sur le projet GitLab, ou une CI job self-hosted `renovate/renovate`) | **Oui** : la panne du Volet A est exactement le scénario que ce fichier était censé empêcher |
| C4 | Commentaire CI mensonger ×2 : « There is no eslint config in the repo (yet) » dans `.gitlab/ci/frontend.yml:2` **et** `.github/workflows/ci.yml:281` — ce dernier 17 lignes au-dessus du `npm run lint` (l.298) qui le contredit | CI | `.gitlab/ci/frontend.yml:2`, `.github/workflows/ci.yml:281` | CI-dev-env | faible — mais un commentaire faux coûte du temps de diagnostic à chaque lecture | S (2 lignes) | **Oui** : correction en minutes, à glisser dans C2 |
| C5 | Pin trivy `0.63.0` (DB de vulnérabilités du scanner qui vieillit) alors que 0.72.0 est publié | CI | `.gitlab/ci/security.yml:20`, `.gitlab/ci/docker.yml:73` | CI-dev-env | faible-moyenne — un scanner en retard rate des CVE récentes | S | **Oui via C3** : c'est exactement le genre de bump que Renovate ferait tout seul ; à la main seulement si C3 traîne |
| C6 | Toolchain dev flottante vs CI pinnée : `.mise.toml` dit `go = "1.26"`, `node = "lts"`, `k3d = "latest"` quand la CI fige `golang:1.26.4` et `node:22.17.0` ; golangci-lint absent de mise alors que l'audit de juillet documentait déjà un incident de divergence local/CI ; `hack/dev/k3d-config.yaml` ne pinne pas l'image k3s | env-dev | `.mise.toml:1-4`, `.gitlab/ci/go.yml:33`, `.gitlab/ci/frontend.yml:5`, `hack/dev/k3d-config.yaml:12` | CI-dev-env | moyenne — « ça passe chez moi, pas en CI » déjà vécu une fois | S (pinner mise sur les versions CI + ajouter golangci-lint ; k3s image dans la config k3d) | **Oui** : une demi-heure pour éliminer une classe d'incident déjà rencontrée |
| C7 | D2 résiduel : `types.ts` redéfinit à la main `RetainedVolume` (l.123) et `RemoteWorkspaceAdmin` (l.265) alors que tygo les génère déjà (`types.gen.ts:271,295`) — la façade exporte la copie manuelle, la version générée est ignorée, et le drift-check CI ne voit rien puisque le fichier généré, lui, est à jour | frontend | `frontend/src/types.ts:123,265`, `frontend/src/types.gen.ts:271,295` | doublon | moyenne — c'est la résurgence exacte de la classe `params: null` que la génération devait tuer ; le prochain champ Go ajouté ne sera visible que dans la copie ignorée | S (supprimer les 2 interfaces manuelles, réexporter depuis types.gen ; balayer le reste des 362 lignes de types.ts pour tout type présent dans model.go) | **Oui** : une heure, et la promesse « types générés » redevient vraie |
| C8 | D3 résiduel : l'union `WorkspacePhase` TS (7 littéraux, `types.ts:82`) recopie l'enum Go sans garde de synchro — tygo génère `phase: string` (`types.gen.ts:198`), donc le drift-check ne couvre pas les phases | frontend / operator | `frontend/src/types.ts:82`, `operator/api/v1alpha1/workspace_types.go:13-25` | doublon | faible-moyenne — une phase renommée côté Go casse les cartes en silence ; même classe de bug que l'incident kasmvnc des protocoles | S (test de synchro style `protocols_sync_test.go`, ou frontmatter tygo pour émettre l'union) | **Oui** : le repo a déjà le modèle exact à répliquer (protocols_sync), c'est un copier-adapter |
| C9 | D4 inchangé : deux implémentations de « ensure Secret copié dans le ns cible » (`kasm_credentials.go` 178 l., `pull_secret.go` 144 l.), sémantiques différentes, squelette identique | operator | `operator/internal/controller/kasm_credentials.go:1`, `pull_secret.go:1` | doublon | faible — aucun incident, les deux sont testés | S-M | **Non** : verdict de juillet toujours valide — factoriser à la 3ᵉ occurrence, pas avant ; le helper générique coûterait plus en lisibilité qu'il ne rapporte |
| C10 | D5 aggravé : la const CSS `'mt-1 w-full rounded-md…'` est passée de 4 à **6 copies** (ProfilePage:61, UsersPage:138 et 246, GovernancePage:449, TemplatesPage:61, RemoteWorkspaceDialog:135) ; `envOr` toujours ×2 (`wwt/cmd/main.go:82`, `config.go:205`) | frontend / wwt | `frontend/src/pages/ProfilePage.tsx:61` (+5), `wwt/cmd/main.go:82` | doublon | faible — cosmétique, mais la copie CSS croît à chaque nouvelle page | S (un `<Input>` partagé ou une const dans `lib/`) | **Discutable** : pas de chantier dédié — l'imposer comme règle au prochain refactor UI (C17 est l'occasion) ; `envOr` : Non, 6 lignes, un package partagé coûterait plus |
| C11 | Login local sans rate-limit ni lockout : `AuthService.Login` vérifie argon2id et audite les échecs, mais rien ne freine un brute-force en ligne (pas de limite par IP ni par compte, pas de backoff) | api-server | `api-server/internal/service/auth_service.go:38`, `handler/auth_handler.go:37` | sécurité | moyenne — mitigée par le coût argon2id (~50 ms/essai), l'audit trail, et l'option OIDC-only qui ferme ce chemin ; élevée si le portail est exposé internet en mode login local | S (middleware `httprate` par IP+username sur `/api/v1/auth/login`) | **Oui** : une heure de middleware pour fermer le vecteur d'attaque le plus banal du produit ; à faire avant toute expo internet en login local |
| C12 | Oracle de timing sur l'énumération de comptes : utilisateur inexistant → retour immédiat (`auth_service.go:40-42`) sans passage argon2id, utilisateur existant → ~50 ms de hash ; l'écart est mesurable à distance | api-server | `api-server/internal/service/auth_service.go:40` | sécurité | faible — il faut déjà vouloir énumérer des usernames, et l'audit ne trace pas ce chemin (pas d'ID) | S (hash factice constant sur le chemin not-found) | **Discutable** : 10 lignes si C11 est fait en même temps, sinon le bénéfice seul ne justifie pas une itération — noter aussi que le chemin not-found n'émet **pas** d'event d'audit, contrairement aux 3 autres échecs |
| C13 | Les secrets Helm sont générés via `lookup` (`secrets.yaml:1-4` : postgres password, internal-token, clé JWT régénérés si absents du cluster) — or `docs/ci.md` désigne ArgoCD comme consommateur du chart, et ArgoCD rend avec `helm template` **sans** accès cluster : `lookup` rend vide → chaque sync régénérerait les 3 secrets (sessions invalidées, mot de passe DB divergent du PVC) | helm | `helm/waas/templates/secrets.yaml:1-4`, `docs/ci.md:5` | sécurité | élevée **si** ArgoCD rend le chart nativement ; nulle si le déploiement réel passe par `helm install` ou un plugin. Non tranché dans le repo — aucun manifest ArgoCD versionné | S (vérifier le mode de rendu réel) puis M (pré-provisionner via ESO/SealedSecrets ou `argocd.argoproj.io/sync-options: Skip` sur le Secret) | **Oui pour la vérification (S)** : si le chemin ArgoCD est réel, c'est un incident de prod garanti au premier sync ; le fix complet attend la réponse |
| C14 | Le chiffre de couverture frontend affiché en CI est le chiffre « naïf » que l'audit de juillet dénonçait déjà : `frontend-test` (`frontend.yml:33`) lance vitest sans `--coverage.all` → 62,4 % (1483 stmts des seuls fichiers importés) contre **40,9 %** réels (2265 stmts) | frontend / CI | `.gitlab/ci/frontend.yml:33`, `.github/workflows/ci.yml:283+` | test | moyenne — un indicateur qui surestime de 21 points oriente mal l'effort de test | S (ajouter `--coverage.all --coverage.include='src/**'` aux deux CI) | **Oui** : deux flags ; sans ça le futur ratchet frontend serait bâti sur un chiffre faux |
| C15 | `DesktopPane.tsx` : 171 statements à 0 % de couverture pour le code le plus délicat du front (canvas Guacamole, clipboard bidirectionnel, resize, fullscreen) — 3 fixes clipboard livrés dessus en une semaine, tous vérifiés à la main | frontend | `frontend/src/components/DesktopPane.tsx:1` | test | moyenne-élevée — chaque fix clipboard/resize se re-vérifie en e2e manuel faute de filet | M (extraire la logique effects → hooks testables : `useClipboardBridge`, `useSessionResize` — `lib/sessionResize.ts` et `lib/clipboard.ts` montrent que le pattern existe déjà et teste bien) | **Oui** : c'est LA zone du front où les régressions sont déjà arrivées ; l'extraction en hooks rend testable sans jsdom-canvas |
| C16 | Zones frontend 0 % restantes : GovernancePage (124 stmts), SplitViewPage (83), ProfilePage (73), UsersPage (48) | frontend | `frontend/src/pages/admin/GovernancePage.tsx`, `pages/SplitViewPage.tsx`, `pages/ProfilePage.tsx`, `pages/admin/UsersPage.tsx` | test | faible-moyenne — CRUD admin classique, moins piégeux que C15 | M | **Discutable** : GovernancePage oui (elle édite la policy, une régression silencieuse touche la gouvernance) ; les 3 autres après C15, au fil des retouches |
| C17 | `TemplatesPage.tsx` = 881 lignes à 31 % de couverture : plus gros fichier du repo front, formulaires multi-onglets + logique protocoles dans un seul composant — même trajectoire que la PortalPage de juillet (1617 l.) qui a fini découpée | frontend | `frontend/src/pages/admin/TemplatesPage.tsx:1` | organisation / test | moyenne — chaque feature protocole retouche ce fichier à l'aveugle | M (découper par onglet comme PortalPage → sections/, tester les formulaires extraits) | **Oui** : le précédent PortalPage prouve que le découpage marche et rend testable ; à faire avant la prochaine feature templates |
| C18 | `workspace_controller.go` 1106 l. (+185 depuis juillet) et `workspace_service.go` 1189 l. + `remote_workspace_service.go` 600 l. : les fourre-tout grossissent malgré des voisins bien découpés (`workload.go`, `placement.go`) | operator / api-server | `operator/internal/controller/workspace_controller.go:1`, `api-server/internal/service/workspace_service.go:1` | organisation | faible — tout est testé et la DI est propre ; c'est un coût de navigation, pas de correction | M | **Discutable** : pas de chantier dédié — imposer « pas de nouvelle responsabilité dans ces fichiers » et extraire au passage de la prochaine feature (lifecycle/status pour le contrôleur) |
| C19 | `shared/auth` stagne à 69,4 % : brique JWT/JWKS partagée par 3 composants, chemins d'erreur (clé inconnue, kid absent, token expiré vs not-yet-valid) partiellement couverts ; `wwt/internal/jwks` à 0 % (client HTTP de fetch JWKS) | shared / wwt | `shared/auth/`, `wwt/internal/jwks/` | test | moyenne — c'est le composant dont TOUTE l'authentification inter-services dépend | S (table-driven sur les chemins d'erreur claims/keys ; un httptest pour jwks) | **Oui** : petit effort, composant à rayon de souffle maximal |
| C20 | `operator/pkg/policy` est la seule couverture en baisse du repo (84 % → 78,7 %) : le code quota/remote-workspaces ajouté depuis juillet est arrivé moins testé que l'existant | operator | `operator/pkg/policy/` | test | faible-moyenne — pkg/policy est la « meilleure décision d'archi du repo » (portail et webhook décident pareil) ; sa couverture doit être exemplaire | S | **Oui** : remonter au niveau antérieur + l'inclure dans le ratchet (il protège l'invariant central) |
| C21 | 4 refs mortes `registry.xorhub.io/waas/waas-images/...` dans le catalogue gitops — le registre réel est `registry.gitlab.com/drummyjohn/waas-images` depuis le split du 10/07 ; flag « en attente du chemin définitif » depuis juillet, or le chemin définitif existe maintenant | gitops | `gitops/governance/images.yaml:11,29,44,60` | organisation | moyenne — un `kubectl apply` de ce catalogue référence des images introuvables ; le custom manager Renovate (`renovate.json`, images kasmweb) ignore ces lignes | S (4 lignes + re-résolution des digests) | **Oui** : le blocage de juillet est levé, il n'y a plus de raison d'attendre |
| C22 | `docs/openapi-governance.yaml` : spec manuelle, partielle, consommée par **rien** (aucune référence hors docs/) et redondante depuis tygo — l'audit de juillet la qualifiait déjà de « doublon qui dérive » | docs | `docs/openapi-governance.yaml:1` | organisation | faible — mais une spec qui ment est pire qu'une absence de spec | S (supprimer, ou décider de la générer — la suppression est le bon défaut tant qu'aucun consommateur n'existe) | **Oui** : suppression en minutes ; garder un doc mort contredit la discipline docs du repo |
| C23 | Vestige racine `fable-waas-build-prompt.md` (brief de bootstrap historique), déjà flaggé en juillet | racine | `fable-waas-build-prompt.md:1` | organisation | faible | S | **Oui** : `git rm`, une minute — ou le déplacer dans `docs/studies/` s'il a valeur d'archive |
| C24 | Hygiène git des docs : `docs/clipboard.md` + 2 studies modifiés non commités, **8 studies non trackés** (11 à 17, 19) — la discipline « la dette vit dans les docs » ne vaut que si les docs sont dans git | docs | `git status` (11-prompt… à 19-prompt…) | organisation | faible-moyenne — une session future ne voit pas ces documents depuis un clone frais | S (un commit docs) | **Oui** : immédiat, et c'est la condition pour que ce rapport-ci serve aux sessions suivantes |
| C25 | a11y toujours non traitée : boutons icônes sans `aria-label` (UserMenu.tsx : 0 aria-label ; SessionCard.tsx : 1) — constat cosmétique de juillet inchangé, désormais partiellement outillable via eslint-plugin-jsx-a11y (eslint existe maintenant) | frontend | `frontend/src/components/UserMenu.tsx:1`, `SessionCard.tsx:1` | organisation | faible — portail interne, pas d'exigence légale identifiée | S (activer jsx-a11y en warn + corriger au fil de l'eau) | **Discutable** : activer la règle eslint coûte une ligne (oui) ; une passe corrective dédiée, non tant qu'aucun besoin utilisateur ne le tire |
| C26 | `guacamole-common-js ^1.5.0` face à guacd 1.6.0 : compatible en pratique, mais l'écart de version reste non documenté dans le repo (la contrainte « source officielle Apache uniquement, pas de miroir npm tiers » n'est écrite nulle part ailleurs que dans une étude) | frontend | `frontend/package.json:18`, `helm/waas/values.yaml:160` | organisation | faible — aucun bug attribué à l'écart | S (un paragraphe dans `docs/templates-and-protocols.md` ou un commentaire package.json actant la politique de version) | **Non** pour un bump (1.6 non publié sur npm par Apache) ; **Oui** pour documenter la contrainte — 15 minutes qui évitent qu'un futur bump « helpful » installe un miroir tiers |
| C27 | OIDC jamais éprouvé contre un IdP réel : le stub (`internal/server`, `stubIdP`) couvre le contrat, pas les excentricités d'un Keycloak/Entra (claims mappés, refresh, clock skew) ; le mode OIDC-only (Feature 14) augmente la dépendance à ce chemin | api-server | `api-server/internal/server/` (stubIdP), `docs/ci.md` | test | moyenne — en OIDC-only, un IdP mal intégré = plus personne ne se connecte | M (un job smoke optionnel avec Keycloak en container, ou une checklist de recette manuelle documentée) | **Discutable** : Oui si des déploiements OIDC-only sont imminents (c'est le cas d'après la feature 14), sinon la checklist manuelle documentée suffit à court terme |

### Constats examinés et écartés (pour tracer le non-trouvé)

- **wwt** : `CheckOrigin: true` est sain ici — le gate réel est le token
  de connexion signé, en query pour `/ws` (`proxy.go:67-80`) et en
  cookie `SameSite=Strict`+`HttpOnly`+path-scopé par session pour
  `/kasm` (`kasm.go:153-160`), cookie strippé vers l'upstream.
- **Tokens comparés en temps constant** partout où ça compte
  (`middleware.go:79`, `password.go:57`).
- **`sslmode=disable`** sur l'URL postgres in-cluster
  (`secrets.yaml:19`) : acceptable pour un postgres du même namespace ;
  ne devient un sujet qu'avec `postgres.externalURL`.
- **Sleeps restants dans les tests** : tous des boucles de poll bornées
  (envtest, smoke, wwt metrics) — pas la classe flaky de juillet.
- **`hack/`** (y compris `hack/dev/`) : rien à remonter — scripts
  `set -eu` shellcheckés en CI, `seed-ssh-secret.sh` documente
  précisément son invariant double-namespace, seeds toujours gardés par
  le vrai webhook, `k3d-config.yaml` explique même comment migrer un
  cluster existant. Seul le non-pin k3s (C6) le concerne.
- **TODO/FIXME** : toujours **zéro** dans le code — la discipline tient.

## 5. Plan d'action (réutilise les colonnes du §4, quick wins d'abord)

| Ordre | Constats | Effort cumulé | Pourquoi cet ordre |
|---|---|---|---|
| 1 | C24 (commit docs) + C4 (2 commentaires) + C23 (vestige) + C22 (openapi mort) | S | Une matinée de ménage ; condition pour que le reste soit exploitable depuis un clone frais |
| 2 | C2 (envtest+eslint+ratchet sur GitLab) + C14 (coverage.all) | S | Rend à la CI réelle les gates déjà écrits — le meilleur ratio du plan |
| 3 | C3 (activer Renovate) puis C5 (trivy bump, absorbé par C3) | S | Ferme la cause racine du Volet A |
| 4 | C7 (types manuels doublonnés) + C8 (garde phases) | S | Achève D2/D3 avec les patterns déjà présents dans le repo |
| 5 | C13 (vérifier le rendu ArgoCD des secrets) | S (vérif) → M (fix si confirmé) | Potentiel incident de prod ; la vérification est triviale, à faire avant tout déploiement ArgoCD |
| 6 | C11 (rate-limit login) + C12 (timing, dans la foulée) + C6 (pins toolchain) | S | Sécurité/repro à faible coût |
| 7 | C19 (shared/auth) + C20 (pkg/policy au ratchet) | S | Couverture des deux briques à plus fort rayon de souffle |
| 8 | C21 (refs gitops) | S | Débloqué depuis le split waas-images |
| 9 | C1 (trancher GitLab vs GitHub) | M | L'arbitrage structurant ; les étapes 2-3 n'en dépendent pas, mais tout double-entretien cesse ici |
| 10 | C15 (DesktopPane en hooks testés) puis C17 (TemplatesPage) | M chacun | Les deux vrais chantiers de test/refactor front, dans l'ordre du risque vécu |
| — | C16, C18, C25, C26 (doc), C27 : au fil de l'eau | — | Verdicts « Discutable » : à raccrocher aux features qui les touchent, pas en chantier dédié |

Non repris : C9, C10-envOr (verdict **Non** — le statu quo est le bon
choix documenté).
