# Prompt Fable 5 — Audit complet du repo (organisation, doublons, sécurité) + triage Trivy/CI

Colle ce document tel quel comme prompt. Il part du principe que tu
(Fable 5) n'as aucun contexte de conversation préalable.

## Objectif

Deux livrables distincts, à ne pas mélanger :

1. **Volet A (action, à faire en premier)** — vérifier l'état réel des
   jobs Trivy en CI et corriger ce qui est trivialement corrigeable
   (bump de dépendance). C'est le seul volet où tu modifies du code.
2. **Volet B (rapport, aucune implémentation)** — audit qualitatif
   complet de **tous** les composants du repo, y compris les tests et
   l'environnement de dev, avec pour **chaque** constat remonté une
   estimation de complexité **et** un verdict explicite "est-ce que ça
   vaut le coup de le faire" — pas seulement pour certains items en fin
   de rapport.

Le rapport final doit être écrit dans un nouveau fichier
`docs/studies/NN-report-…md` (vérifie `ls docs/studies` pour le
prochain numéro libre — au moment de la rédaction de ce prompt le
dernier fichier est `18-prompt-feature14-oidc-only-login.md`, donc `19`
est probablement libre, mais ne le suppose pas sans vérifier).

## Point de départ : un audit existant, à revalider, pas à recopier

`docs/studies/audit-2026-07.md` (2026-07-08) est un audit complet du
même type, composant par composant, avec chiffres de couverture mesurés
et constats sourcés fichier+ligne. **Ne repars pas de zéro** : lis-le
d'abord, et pour chaque constat "Important"/"Cosmétique" encore listé,
vérifie s'il est toujours vrai avant de le recopier dans le nouveau
rapport.

**Preuve que ça a déjà bougé depuis** : l'audit du 8 juillet liste en
finding "Important" du Frontend — *"Aucun eslint/prettier (`tsc -b` est
le seul lint)"*. Ce n'est déjà plus vrai : `frontend/eslint.config.js`
existe, `frontend/package.json` a un script `"lint": "eslint ."` et
`.github/workflows/ci.yml:298` exécute `npm run lint` dans la CI
GitHub (canonique d'après ce même audit). Le seul endroit où c'est
encore faux est un **commentaire obsolète**,
`.gitlab/ci/frontend.yml:2` : *"There is no eslint config in the repo
(yet): tsc -b is the lint gate."* — lui-même un petit constat à
remonter (commentaire CI qui ment sur l'état du repo). Traite ça comme
le mode opératoire général : chaque ligne de l'audit du 8 juillet est
une hypothèse à re-tester, pas un fait acquis. Note explicitement dans
le nouveau rapport ce qui a changé depuis (corrigé, encore ouvert,
aggravé).

Commits pertinents depuis le 8 juillet (`git log --oneline` sur `main`
te donnera la liste exacte) : split de `waas-images/` hors du monorepo,
clipboard guacd + HTTPS dev, clipboard KasmVNC, scope owner-only des
workspaces, unification YamlEditor, groupement fleet par owner, feature
"direct image deploy", feature "OIDC-only login", push du chart Helm en
artefact OCI. Vérifie l'impact de chacun sur les constats de l'audit
précédent.

## Périmètre — TOUS les composants, y compris tests et env de dev

Traite chacun de ces éléments comme un composant de première classe
(pas de note en bas de page pour les deux derniers, c'est explicitement
demandé) :

- `operator/` (`internal/`, `pkg/`, `api/`, **et `operator/test/` +
  `operator/hack/`**)
- `api-server/` (`internal/`, `migrations/`, `cmd/`)
- `frontend/` (`src/`, **et tous les `*.test.tsx`/`*.test.ts`
  existants**)
- `wwt/`
- `shared/`
- `hack/` — **y compris `hack/dev/`** (config k3d, seeds, images/
  templates dev, `seed-ssh`) : c'est l'environnement de dev, à auditer
  au même titre que le code de prod, pas à ignorer parce que "c'est du
  dev"
- `test/smoke/` (e2e k3d)
- `gitops/governance/`
- `helm/waas/`
- `.mise.toml` + `Makefile` (cibles `dev-*` en particulier) — pins de
  toolchain, reproductibilité de l'environnement de dev
- **La CI elle-même comme composant audité** : `.gitlab-ci.yml` +
  `.gitlab/ci/*.yml`, `.github/workflows/ci.yml`, `renovate.json` —
  doublons entre les deux CI (GitLab en transition d'après l'audit
  précédent — vérifie où en est cette transition), dérive
  commentaire/réalité comme l'exemple eslint ci-dessus
- `docs/` — repère la documentation orpheline ou contradictoire avec le
  code actuel (ex. un ADR ou un `docs/studies/*` qui décrit un
  comportement depuis changé)

## Volet A — Trivy / CI : vérifier et corriger si trivial

### Comment vérifier l'état réel

`glab` est authentifié sur ce projet dans cet environnement. Utilise-le
directement plutôt que de deviner l'état de la CI :

```
glab ci status -b main
glab api projects/:id/pipelines/<id>/jobs      # liste des jobs + statut
glab api projects/:id/jobs/<job-id>/trace       # logs d'un job précis
```

(`glab ci view` ne marche pas hors TTY interactif — utilise `glab ci
status`/`glab api` comme ci-dessus dans ce contexte.)

### État constaté au moment de la rédaction de ce prompt (2026-07-11)

Pipeline `#14` (sha `33da60999c92`, branche `main`) : jobs **`trivy-deps`
(stage `security`) et `scan-frontend`/`scan-api-server`/`scan-operator`
(stage `scan`) tous en FAILED**. Cause identique sur les 4 : gate
`trivy fs --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed`
(`.gitlab/ci/security.yml:29-32`) qui échoue sur des CVE **avec fix
disponible** :

- `golang.org/x/net` en `v0.47.0` dans `api-server/go.mod:72`,
  `operator/go.mod:50`, `test/smoke/go.mod:33` (toutes en ligne
  `// indirect`) — CVE-2026-25681, CVE-2026-27136, CVE-2026-33814,
  CVE-2026-39821, toutes HIGH, fix ≥ `0.55.0`.
- `golang.org/x/crypto` en `v0.45.0` dans `api-server/go.mod:15`
  (dépendance directe) — plusieurs CVE HIGH sur `x/crypto/ssh` (dont
  CVE-2026-39828…39835, CVE-2026-42508, CVE-2026-46595, CVE-2026-46597),
  fix ≥ `0.52.0` pour la première vague — **revérifie la dernière
  version corrigée au moment où tu exécutes ce prompt**, le paysage CVE
  bouge en continu et une nouvelle CVE peut être apparue depuis.

`wwt/go.mod` et `shared/go.mod` ne référencent pas ces deux libs à la
date de rédaction — vérifie que ça reste vrai après un `go mod tidy`
sur les modules touchés (le graphe de dépendances peut changer).

### Action attendue

1. Pour chaque `go.mod` concerné (`api-server`, `operator`,
   `test/smoke`, et tout autre module du `go.work` qui s'avérerait
   touché) : vérifie d'abord s'il existe déjà une **MR Renovate
   ouverte** pour ce bump (`renovate.json` a `config:recommended`, le
   manager `gomod` est actif par défaut — ce bump devrait normalement
   être proposé automatiquement). Si une MR existe, préfère la
   reprendre/fusionner plutôt que de dupliquer le travail ; si elle
   n'existe pas ou est bloquée, fais le bump toi-même.
2. `go get -u golang.org/x/net golang.org/x/crypto@latest` (ou version
   ciblée si `@latest` casse une contrainte ailleurs) puis `go mod
   tidy` dans chaque module touché. `go.work.sum` à régénérer si
   nécessaire.
3. Build + tests du module touché (`go build ./...`, `go test ./...`)
   avant de considérer le fix acquis — un bump de `x/net`/`x/crypto`
   est généralement sans API breaking mais vérifie quand même
   (`x/crypto/ssh` a un historique de signatures qui bougent).
4. Reproduis le scan en local pour confirmer
   (`trivy fs --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed
   --exit-code 1 .` à la racine, image `aquasec/trivy:0.63.0` comme en
   CI) ou relance la pipeline si tu préfères valider via GitLab
   directement.
5. Le job `trivy-deps` scanne aussi `package-lock.json`
   (`.gitlab/ci/security.yml:30`) — vérifie s'il y a des CVE npm
   HIGH/CRITICAL non capturées par le run que tu viens d'inspecter (le
   run inspecté au moment de la rédaction ne montrait que des CVE Go,
   mais reconfirme).
6. Commit séparé pour ce fix, distinct du rapport d'audit (Volet B) —
   ce sont deux changements de nature différente, ne les mélange pas
   dans le même commit.

### Ce qu'il ne faut PAS faire

- Ne touche pas `TRIVY_SEVERITY`/`TRIVY_EXIT_CODE` ni les scanners pour
  faire taire l'échec — le commentaire en tête de
  `.gitlab/ci/security.yml:1-3` est explicite : ces variables sont pour
  la gestion d'incident, jamais pour être posées dans le YAML. Le seul
  fix acceptable est la vraie remédiation (bump).
- Si une CVE n'a **pas** de fix disponible à la date d'exécution, ou si
  le bump casse quelque chose de non trivial (API breaking, contrainte
  de version conflictuelle) : **ne force pas**. Consigne-la comme un
  constat normal du Volet B, avec sa complexité et son verdict "vaut le
  coup", au lieu de bidouiller un contournement.

## Volet B — Audit qualitatif, méthode et format

Même standard de rigueur que `docs/studies/audit-2026-07.md` : chaque
constat est **sourcé fichier+ligne**, les chiffres de couverture sont
**mesurés** (lance les outils de coverage réels par composant), jamais
estimés au jugé. Aucune implémentation dans ce volet, uniquement le
rapport (le seul code que tu touches dans ce prompt est le bump du
Volet A).

Pour chaque composant du périmètre ci-dessus, cherche :

- **Doublons** — logique/données dupliquées entre fichiers ou entre
  composants (le précédent audit en a trouvé 5, avec au moins un cas
  ayant déjà causé un incident réel — vérifie si D1 à D5 sont toujours
  d'actualité et cherche-en de nouveaux, en particulier dans le code
  ajouté depuis le 8 juillet).
- **Organisation** — fichiers/fonctions trop gros, découpage
  contestable, conventions non respectées par rapport au reste du
  composant.
- **Sécurité** — au-delà de Trivy (qui ne couvre que les CVE de
  dépendances) : autorisation, validation d'entrée, secrets, timeouts
  réseau, tout ce qu'un audit de sécurité applicatif couvrirait
  normalement.
- **Tests** — couverture réelle mesurée par package/composant, qualité
  (tests qui testent le mock plutôt que le comportement ?),
  flakiness connue (le précédent audit a noté un `time.Sleep(100ms)`
  dans `event_hub_test.go:77` — vérifie s'il y en a d'autres), zones
  non couvertes du tout. **Traite l'écart de couverture comme un
  composant à part entière du rapport**, pas une ligne noyée dans
  chaque section.
- **Environnement de dev** (`hack/dev/`, `.mise.toml`, cibles `Makefile
  dev-*`) — reproductibilité, pins de version, scripts non documentés,
  divergence avec ce que la CI utilise réellement.
- **Dette documentée mais non traitée** — le précédent audit notait que
  ce repo n'a aucun TODO/FIXME (la dette vit dans les docs). Vérifie si
  cette discipline tient toujours, et si les items de dette qu'il
  listait (N+1 non corrigé, refs `registry.xorhub.io` obsolètes, etc.)
  sont résolus ou pas.

### Format obligatoire de chaque constat

Un tableau, une ligne par constat, **ces colonnes exactement** — la
colonne "Vaut le coup ?" est remplie pour **chaque** ligne, jamais
laissée implicite ou reportée à un tableau de synthèse à part :

| # | Constat | Composant | Source (fichier:ligne) | Catégorie | Gravité/risque | Complexité | Vaut le coup ? |
|---|---|---|---|---|---|---|---|
| … | (une phrase, le problème concret) | operator / api-server / frontend / … | `path/to/file.go:123` | doublon / organisation / sécurité / refactor / test / CI-dev-env | faible / moyenne / élevée + pourquoi | S (< 1 j) / M (1-3 j) / L (3-5 j) / XL (> 5 j) — estimation grossière, pas un chiffrage précis | Oui / Non / Discutable — **une phrase de justification obligatoire**, pas juste le mot |

"Vaut le coup ?" doit trancher, pas décrire — ex. "Non : cosmétique,
aucun incident vécu, le renommage casserait 4 call-sites pour un
bénéfice nul" plutôt que "dépend du contexte".

### Synthèse finale

Après le tableau détaillé, une section plan d'action triée (quick wins
d'abord) qui **réutilise** les colonnes Complexité/Vaut le coup déjà
posées ligne par ligne — ne réinvente pas une deuxième estimation
contradictoire dans cette section, comme le faisait implicitement le
tableau "Plan" de `audit-2026-07.md` §5.

## Contraintes

- Volet A = seul endroit où tu modifies du code produit ; Volet B =
  rapport pur.
- N'affaiblis jamais un gate de sécurité (seuils Trivy, scanners) pour
  faire passer la CI.
- Ne duplique pas une MR Renovate déjà ouverte pour le même bump.
- Chaque constat du rapport doit être exploitable seul par une future
  session Fable sans relire cette conversation — assez de détail
  fichier:ligne pour qu'un futur prompt `docs/studies/NN-prompt-*.md`
  n'ait pas à refaire la recherche depuis zéro (c'est explicitement le
  critère de "rapport facilement utilisable par la suite").

## Points ouverts (ton arbitrage)

- Numéro exact du fichier de sortie (`docs/studies/NN-report-…md`) —
  vérifie le dernier numéro libre au moment de l'exécution.
- Granularité composant vs sous-dossier pour les constats "organisation"
  (ex. un service de 900 lignes vaut-il un constat par service ou un
  constat groupé "services fourre-tout") — libre, documente le choix.
- Si une CVE Trivy n'a aucun fix disponible à la date d'exécution :
  consigne-la comme risque accepté documenté dans le rapport plutôt que
  de bloquer le Volet A dessus.
