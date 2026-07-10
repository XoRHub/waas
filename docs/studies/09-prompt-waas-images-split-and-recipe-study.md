# Prompt Fable 5 — waas-images : split en repo dédié + étude de simplification (recette d'images, nouveaux OS/apps, variante hardened/dev)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable. Ce prompt a deux natures différentes — **traite-les comme
telles** :

- **Partie A (split)** : une action mécanique, bien spécifiée, à
  livrer entièrement dans cette session.
- **Partie B (étude)** : une livraison **documentaire uniquement**.
  N'implémente rien de ce qu'elle propose — pas de nouveau Dockerfile,
  pas de nouvelle CI, pas de variante hardened. Le repo a déjà un
  précédent pour ce découpage (`docs/studies/prompt-etude1-protocol-feature-matrix.md`
  a produit une étude pure ; chaque item arbitré est devenu son propre
  prompt numéroté 01 à 08 ensuite) — suis le même schéma. Une fois
  l'étude livrée et ses points ouverts tranchés par l'utilisateur, un
  **prompt de suivi séparé** pilotera l'implémentation retenue — ce
  n'est pas à toi de la déclencher ici.

## Contexte du repo

`waas-images/` (racine du monorepo) n'est **pas un point de départ
vierge** : c'est déjà un système de build d'images OCI layered et
fonctionnel, livré et itéré sur plusieurs commits (`d7c2b0996802`
initial, puis `b98a6dd9d`, `d49ebfd07`, `61c8b29d1`, `6326b0c24`
jusqu'à aujourd'hui). Avant de proposer quoi que ce soit en partie B,
lis en entier :

- `waas-images/README.md` — contrat avec le Workspace CR, matrice de
  build, procédure « Adding an app image » (§ 4 étapes, lignes
  113-141).
- `waas-images/images.yaml` — config globale (matrice OS, archs par
  défaut, gate trivy).
- `waas-images/HARDENING.md` — checklist vérifiable point par point
  (build-time / runtime / côté plateforme) + threat model + gaps
  acceptés documentés.
- `waas-images/ci/generate_pipeline.py` — découvre les `manifest.yaml`
  sous `base/`, `desktop/`, `apps/`, trie topologiquement sur `from`,
  génère un pipeline enfant GitLab (un job de build natif par image ×
  arch + un job de merge de manifest-list).
- Un `manifest.yaml` existant (`waas-images/apps/firefox/manifest.yaml`
  par ex.) pour voir le format de recette actuel.

## Ce qui existe déjà (à connaître avant de proposer quoi que ce soit)

- **Le format « recette » existe déjà**, sous deux fichiers : un
  `manifest.yaml` par image (nom, version, `from:` le parent, variants,
  attentes de smoke) + `images.yaml` global (matrice OS, archs,
  sévérité trivy). Ajouter une image = un nouveau dossier + Dockerfile
  - manifest, aucune modification du générateur. Ce n'est **pas** un
    vide à combler par un système entièrement nouveau — l'étude doit
    juger si ce format suffit déjà à la barre « recette de cuisine »
    demandée, ou identifie précisément ce qui manque (un Dockerfile
    reste requis pour chaque nœud de la couche `apps/` — voir § B.1).
- **supervisord est déjà l'orchestrateur runtime** (`tini + supervisord`,
  README.md:24) ; **cloud-init n'apparaît nulle part** dans le repo —
  c'est un outil de provisioning de VM/instance cloud, pas d'image
  conteneur ; ne le propose pas par réflexe simplement parce qu'il est
  cité dans la demande, vérifie s'il a un rôle réel à ce build-time là.
- **La CI matricielle existe déjà, mais GitLab seulement.**
  `docs/ci-github.md:90` documente explicitement ce trou : « waas-images/ :
  pipeline enfant GitLab généré, pas d'équivalent GitHub. » —
  `docs/ci-github.md:36` idem. Ce n'est pas une supposition, c'est déjà
  écrit noir sur blanc comme gap connu.
- **Un gap de catalogue déjà documenté, pas à inventer** :
  `gitops/governance/images.yaml:53-70` référence une image
  `ubuntu-devtools` (« Dev Tools (VS Code, toolchains) », restreinte au
  groupe IdP `nymphe:dev` via `allowedGroups`) — mais **aucun
  `waas-images/apps/devtools/` n'existe** dans l'arbre. `hack/dev/images-dev.yaml:5`
  le confirme explicitement : « ubuntu-devtools in the real catalog has
  no local manifest yet ». C'est le candidat naturel pour un des « une
  ou deux apps en plus » demandés — il ferme un trou déjà annoncé au
  lieu d'en inventer un nouveau.
- **Le hardening actuel est incompatible avec un usage sudo/dev en
  l'état**, par construction et pas par oubli : `HARDENING.md` impose
  « No setuid/setgid binaries: all `+s` bits stripped » et
  « Read-only rootfs compatible », vérifiées par le smoke test CI
  (`--read-only --cap-drop ALL --security-opt no-new-privileges`,
  README.md:109-111). `sudo` est un binaire setuid par nature et a
  besoin d'écrire (apt) hors de `/home/user`/`/tmp`/`/run` — la piste
  déjà utilisée ailleurs (`RDP_AUTH_ENABLED`, un flag **runtime** qui
  relâche une posture) ne s'applique pas ici : on ne peut pas activer
  sudo après coup sur une image sans le binaire setuid ni un rootfs
  inscriptible. Une variante « moins durcie » est nécessairement un
  **choix de build**, pas un flag d'environnement — à documenter comme
  tel en partie B.
- **Le split touche des points d'ancrage réels dans le monorepo**,
  recensés ici pour éviter un audit répété en partie A :
  - Root `.gitlab-ci.yml` : job `waas-images:` (`trigger: include:
waas-images/.gitlab-ci.yml`, stage `build`, déclenché sur
    `changes: [waas-images/**/*]`).
  - Root `Makefile:140-145` (`dev-build-images`) : `$(MAKE) -C
waas-images build IMAGE=$$img` — c'est le cœur de la boucle
    `dev-bootstrap`/`dev-reload-all` livrée par
    `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md`. Un
    split naïf **casse cette boucle silencieusement**.
  - `release-please-config.json:7` anticipe déjà la sortie de
    `waas-images/` du monorepo (« waas-images/ is out of scope (own
    per-image versioning) ») — la mention doit être mise à jour une
    fois le dossier physiquement parti, pas juste laissée telle quelle.
  - Docs à jour : `docs/ci.md` (lignes 23, 57, 99, 143, 145),
    `docs/ci-github.md` (36, 90), `docs/governance.md:199`,
    `README.md:51` (racine), `hack/dev/images-dev.yaml:4-5`.
  - Commentaires de code pointant `waas-images` comme convention (pas
    de lien fonctionnel, à laisser tels quels ou reformuler sans les
    faire disparaître) :
    `operator/api/v1alpha1/workspacetemplate_types.go:198,319,381`,
    `operator/api/v1alpha1/workspaceimage_types.go:54`.
  - **Ce qu'il NE FAUT PAS toucher** : les coordonnées d'image publiées
    dans `gitops/governance/images.yaml` (`registry.xorhub.io/waas/waas-images/ubuntu-xfce:1.0.0`,
    etc.) — c'est l'adresse d'un artefact déjà publié, indépendante de
    l'emplacement de l'arbre source. Les renommer est une décision
    séparée, plus risquée (tags pinnés), hors scope de ce prompt.

## Ce qu'il faut livrer

### A. Split — à exécuter entièrement (obligatoire, en premier)

1. **Préserve l'historique.** Utilise `git filter-repo` (pas `git
subtree split`, plus lent et moins fiable sur un monorepo de cette
   taille) sur un **clone jetable** du repo courant, filtré sur
   `waas-images/` avec un `path-rename` qui remonte son contenu à la
   racine. Ne touche jamais le clone de travail de l'utilisateur pour
   cette étape — opère dans un répertoire temporaire, puis initialise
   le nouveau repo à l'emplacement final.
2. **Emplacement** : le nouveau repo doit se retrouver dans
   `../waas-image` — un répertoire frère de la racine de ce monorepo
   (chemin littéral demandé par l'utilisateur, au singulier, alors que
   l'arbre interne et toute la documentation continuent de s'appeler
   `waas-images` partout ailleurs : titres, chemins de registre déjà
   publiés, commentaires). **Ne renomme rien à l'intérieur** de l'arbre
   déplacé sur cette seule base — documente le choix (dossier externe
   au singulier, convention interne inchangée) comme point ouvert
   plutôt que de trancher silencieusement dans un sens ou l'autre.
3. **Retire `waas-images/` du monorepo** avec un commit simple
   (`git rm -r waas-images`), **sans réécrire l'historique du
   monorepo** — celui-ci garde la mémoire du dossier, seul le futur
   change.
4. **Répare chaque point d'ancrage** listé ci-dessus dans « Ce qui
   existe déjà » :
   - `.gitlab-ci.yml` racine : décide et implémente (voir « Points
     ouverts ») soit la suppression pure du job `waas-images:`, soit un
     trigger cross-repo (`include: project:, file:` / pipeline
     multi-projet GitLab) qui pointe vers le nouveau repo.
   - `Makefile` racine : `dev-build-images` doit résoudre le nouveau
     repo à un chemin configurable (`WAAS_IMAGES_DIR ?= ../waas-image`)
     et **échouer avec un message actionnable** (pas une erreur make
     cryptique) si le répertoire est absent — indique la commande de
     clone attendue dans le message d'erreur plutôt que de cloner tout
     seul (un clone déclenche du réseau et de l'écriture disque non
     demandés explicitement).
   - Toute la doc listée ci-dessus (`docs/ci.md`, `docs/ci-github.md`,
     `docs/governance.md`, `README.md` racine,
     `hack/dev/images-dev.yaml`, `release-please-config.json`).
5. **Bootstrap du nouveau repo** : README/CI/Makefile existants
   suffisent tels quels au démarrage (ils sont déjà autonomes — le
   Makefile de `waas-images/` ne dépend de rien hors de son propre
   dossier). Ajoute une licence si la racine du monorepo en a une et
   que son type doit se propager — vérifie avant de supposer.
6. **Ne pousse aucun remote nouveau sans confirmation explicite** :
   crée le répertoire local `../waas-image` et son commit initial, mais
   arrête-toi avant tout `git remote add` / `git push` vers une forge
   — l'espace de nommage cible (même org GitLab, nouveau projet GitHub,
   double publication) est un point ouvert, pas une évidence
   technique.

### B. Étude — livrable documentaire uniquement (dans le nouveau repo, `../waas-image/docs/RECIPE-STUDY.md`)

Écris une étude au même niveau d'exigence que
`docs/studies/kasm-images-feasibility.md` ou
`docs/studies/protocol-feature-matrix-2026-07-10.md` de ce monorepo
(constats vérifiés, sources citées, recommandation assumée mais pas
imposée — les décisions finales reviennent à l'utilisateur). Couvre,
dans cet ordre :

1. **Simplification du format recette.** Le système actuel
   (`images.yaml` + `manifest.yaml` + Dockerfile à la main par nœud)
   couvre-t-il déjà le besoin « recette de cuisine OS/applications/app
   seule en YAML » ou manque-t-il un compilateur déclaratif
   (`recipe.yaml` → Dockerfile généré) au-dessus de la couche `apps/` ?
   Si tu proposes ce compilateur, évalue explicitement, sans réflexe
   d'ajout de dépendance :
   - **cloud-init** — juge s'il a un rôle réel à un build-time
     d'image conteneur (probablement non : c'est un outil de
     provisioning VM/instance, pas de build OCI) ; documente le
     verdict au lieu de l'écarter sans preuve ni l'ajouter sans
     justification.
   - **supervisord** — déjà l'orchestrateur runtime du repo ; un
     fragment de conf supervisord par app pourrait-il remplacer le
     Dockerfile pour le cas « app seule, pas de desktop » ?
   - **script maison** — cohérent avec la philosophie déjà en place
     (pas de dépendance nouvelle si évitable, cf. HARDENING.md) : un
     petit générateur Dockerfile-from-YAML fait-il mieux que les 4
     étapes manuelles actuelles (README.md:113-141) sans réintroduire
     de la complexité que le système actuel évite déjà ?
     Recommande une option, avec compromis assumés — pas une liste
     neutre de possibilités.
2. **CI GitHub Actions matricielle**, en miroir du pipeline GitLab
   généré par `ci/generate_pipeline.py` (même tri topologique
   base→desktop→apps, mêmes gates smoke/trivy/cosign par image × arch).
   Comble le gap déjà documenté en `docs/ci-github.md:90` de ce
   monorepo — vérifie s'il faut le dupliquer/adapter dans le nouveau
   repo une fois séparé. Propose le design (jobs, matrix, réutilisation
   ou non de `generate_pipeline.py`) ; n'écris pas le YAML final dans
   cette étude, ce sera livré dans le prompt de suivi arbitré.
3. **Nouveaux OS/applications**, un ou deux suffisent, un nouvel OS de
   base au minimum :
   - **`ubuntu-devtools`** en priorité — ferme un gap déjà annoncé côté
     gouvernance (`gitops/governance/images.yaml:53-70`, restriction
     `allowedGroups: [nymphe:dev]` déjà écrite) et déjà signalé manquant
     (`hack/dev/images-dev.yaml:5`), plutôt qu'une proposition
     spéculative.
   - Une application supplémentaire de ton choix (justifie : utilité
     réelle pour un poste WaaS, complexité d'ajout raisonnable —
     code-server/VS Code web et un second navigateur sont des pistes
     plausibles, sans obligation).
   - Un nouvel OS de base : évalue le delta réel depuis
     `base/ubuntu/Dockerfile` pour le candidat proposé (Debian étant le
     plus proche techniquement, apt-based comme Ubuntu) — ne le propose
     que si le coût de maintenance réel (paquets équivalents, xrdp,
     TigerVNC packagés) a été vérifié, pas supposé.
4. **Variante hardened/moins-hardened au build time**, pour les
   environnements dédiés au développement où l'utilisateur a besoin de
   `apt install`/sudo en session. Comme établi ci-dessus, ça ne peut
   pas être un flag runtime — propose le mécanisme de build (nouveau
   build-arg type `INSTALL_SUDO=1`, tag distinct `-dev`, sudoers
   NOPASSWD pour l'UID 1000, rootfs non read-only pour ce tag
   spécifiquement) et son inscription dans `HARDENING.md` comme profil
   de sécurité **réduit et documenté**, pas une régression silencieuse
   de la checklist actuelle. Propose aussi comment l'empêcher de fuiter
   par accident vers la population générale — le repo a déjà un
   précédent utilisable (`allowedGroups` sur `ubuntu-devtools`,
   `gitops/governance/images.yaml:64-66`).

Termine l'étude par une section **« Points ouverts »** qui reprend,
sous forme de liste tranchable, chacun des choix ci-dessus (format
recette, inclusion cloud-init, cible GitHub Actions, OS candidat,
2ᵉ app candidate, mécanisme exact de la variante dev) — c'est ce que
l'utilisateur arbitrera avant le prompt de suivi.

## Contraintes à respecter

- Partie A doit laisser le monorepo et le nouveau repo tous les deux
  dans un état qui build/lint sans erreur : pas de job CI cassé par un
  `include` pointant vers un chemin qui n'existe plus, pas de cible
  Makefile qui échoue silencieusement (`No rule to make target`).
- Partie B ne modifie ni Dockerfile, ni manifest, ni pipeline CI — texte
  seulement. Résiste à la tentation d'implémenter une proposition « pendant
  que tu y es » : ce n'est pas arbitré.
- Ne renomme ni ne déplace les coordonnées de registre déjà publiées
  (`gitops/governance/images.yaml`) dans ce prompt.
- Aucun `git push` vers un nouveau remote sans confirmation explicite
  de l'utilisateur.
- Toute décision de la partie A qui a une alternative raisonnable
  (suppression vs trigger cross-repo du job GitLab ; nommage singulier
  du dossier externe vs convention interne) doit être documentée dans
  le message de commit ou l'étude, pas juste tranchée en silence.

## Points ouverts (ton arbitrage)

- Suppression pure du job `waas-images:` dans le `.gitlab-ci.yml`
  racine vs. trigger cross-repo vers `../waas-image` désormais externe
  — dépend de la forge/organisation cible, à confirmer avant de coder.
- Espace de nommage du nouveau remote (même org GitLab, nouveau projet
  GitHub, double publication) — condition la réponse à la question CI
  GitHub Actions de la partie B.
- Nom du dossier externe (`waas-image` singulier, demandé tel quel) vs.
  convention interne inchangée (`waas-images` partout dans le code/doc
  de l'arbre déplacé) — à assumer explicitement, pas à uniformiser
  sans le dire. arbitrage le dossier waas-images existe deja et a deja un git init d'effetuer au chemin ~/Documents/Personal/Projects/XorHub/waas-images/
- Compilateur de recette (cloud-init/supervisord/script maison) — ton
  jugement en étude B.1, pas une évidence technique.
- Mécanisme exact de la variante dev/moins-hardened (build-arg précis,
  nom de tag, gating catalogue) — ton jugement en étude B.4.
