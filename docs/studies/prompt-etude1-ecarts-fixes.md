# Prompt Fable 5 — combler les écarts UI/réalité de l'étude protocole × fonctionnalité

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte

`docs/studies/protocol-feature-matrix-2026-07-10.md` (livrée 2026-07-10) croise les 4 chemins de connexion (VNC/RDP/SSH via guacd, KasmVNC via reverse-proxy HTTP brut) avec les fonctionnalités transverses, entièrement sourcée code. Sa section finale, **« Écarts vs. ce que l'UI laisse penser »**, liste 6 endroits où le portail promet quelque chose que le système ne tient pas (ou l'inverse). Ce prompt les traite dans un ordre de complexité croissante, pas dans l'ordre de gravité du document source — lis quand même la section source avant de coder, elle contient les preuves (fichier:ligne) que ce prompt résume.

Traite chaque tâche comme un incrément indépendant, commit séparé si possible. Les tâches A et B ne touchent aucun fichier commun avec C/D/E. C, D et E touchent toutes `waas-images/` mais des chemins distincts (VNC-audio pour C, xrdp/sesman pour D, aucun fichier image pour E — E est backend + frontend).

**Décisions déjà arbitrées (ne les rouvre pas)** :
- KasmVNC est explicitement refusé sur les remote workspaces (tâche A).
- Le clipboard/audio RDP fait l'objet d'une investigation architecturale avant tout code (tâche D) — implémente seulement si tu peux le faire sans regresser le hardening documenté ; sinon documente et arrête-toi.
- Le resize dynamique (tâche E) doit être un mécanisme réel de bout en bout, pas un simple `sendSize()` frontend — l'architecture actuelle ne le supporterait pas (voir tâche E).

---

## Tâche A (la plus simple) : refuser KasmVNC sur les remote workspaces

**Écart #5 de l'étude.** Le code accepte aujourd'hui kasmvnc comme protocole d'un remote workspace (`api-server/internal/service/remote_workspace_service.go:174`, `normalizeRemoteProtocols` valide contre `params.Protocols()` sans exclusion), la résolution applique `kasmDefaults` (`workspace_service.go:810`), et le frontend route `kind=remote` + `kasmvnc` vers l'iframe (`DesktopPane.tsx:149`). Mais ni la doc (`docs/remote-workspaces.md:4-5`) ni le commentaire du modèle (`api-server/internal/model/model.go:82-84`, « reachable through guacd ») ne mentionnent kasmvnc, et ce croisement n'est exercé par aucun test — **jamais vérifié en session live**. Décision : kasmvnc reste un protocole in-cluster uniquement (le reverse-proxy `wwt/internal/kasm` cible un serveur KasmVNC co-localisé dans le cluster ; la sémantique « machine externe » n'a pas d'équivalent kasm sans une brique supplémentaire, et personne ne peut garantir aujourd'hui que ça marche).

**À faire** :
1. Dans `normalizeRemoteProtocols` (`remote_workspace_service.go:174`), exclure explicitement `kasmvnc` de la liste des protocoles acceptés pour un remote workspace : `slices.Contains(params.Protocols(), e.Name) && e.Name != "kasmvnc"`, avec un message d'erreur explicite (« kasmvnc is not supported for remote workspaces »).
2. Applique la même exclusion partout où un protocole de remote workspace est validé au connect (`in.Protocol`, cf. le bloc autour de `remote_workspace_service.go:396-399` qui appelle `params.ValidateTemplateParams`) — vérifie s'il y a un point d'entrée unique ou s'il faut dupliquer le garde ; si tu dupliques, factorise dans une petite fonction plutôt que de copier la condition.
3. Ajoute un test (`remote_workspace_service_test.go`) : enregistrer un remote workspace avec `protocol: "kasmvnc"` doit échouer en 400.
4. Le message d'erreur remonte tel quel au frontend (`apierror.BadRequest`) — pas besoin de filtrer côté UI en plus, sauf si tu constates qu'un formulaire propose déjà kasmvnc dans un select de remote workspace (vérifie `RemoteWorkspaceDialog.tsx`) ; si oui, retire l'option de la liste plutôt que de laisser l'utilisateur se prendre un 400 après coup.

Effort : petit (~1h avec tests), backend uniquement, zéro changement de contrat pour VNC/RDP/SSH remote.

---

## Tâche B : masquer l'overlay clipboard sur les sessions kasmvnc

**Écart #1, le plus grave de l'étude.** `SessionOverlay.tsx` (section « clipboard (live) », autour de la ligne 244) rend les toggles copier/coller et l'échange manuel pour toute session sans distinction de protocole. Sur une session kasmvnc, ces contrôles sont des no-op silencieux : `tunnelRef.current` est nul sur la branche kasm (`DesktopPane.tsx:95` + branche kasmvnc `:149`), `sendClipboardRef` (`:59`) n'est jamais réassigné hors du chemin guac (seulement `:231`), et le vrai presse-papiers vit dans l'iframe KasmVNC, hors de portée de la policy (`wwt/internal/kasm/kasm.go`, reverse-proxy pur sans inspection). C'est l'inverse de la décision v1 assumée (« affichage honnête », `docs/studies/kasm-images-feasibility.md:86`).

**À faire** : la variable `protocol` existe déjà au niveau du composant (`const protocol = protoSwitch.active`, `SessionOverlay.tsx:105`) — utilise-la pour conditionner le rendu de la section clipboard (bloc autour de la ligne 244) : si `protocol === 'kasmvnc'`, ne rends ni les toggles copier/coller ni le bloc d'échange manuel. Remplace par soit rien (section absente), soit un texte court expliquant que le presse-papiers de cette session est géré par le client KasmVNC lui-même, hors de la policy WaaS — choisis la première option si tu n'as pas de nouvelle clé i18n à justifier pour un message qui ne sert qu'un seul protocole, la seconde si tu penses qu'un utilisateur serait surpris par l'absence totale de la section.

**Points d'attention** :
- Ne touche pas à `hasClipboardApi()` ni au reste de l'overlay (pause/wake/resize/params) — uniquement la section clipboard.
- Si `capabilities.clipboardCopy`/`clipboardPaste` restent `true` pour une session kasmvnc (la policy peut accorder un droit clipboard sur un protocole qui ne peut pas l'enforcer), ne corrige pas ça silencieusement dans ce prompt — documente-le comme écart supplémentaire dans le commit.
- Ajoute un test component (`SessionOverlay.test.tsx` s'il existe, sinon crée-le à côté en suivant la convention des autres tests de composants) : une session kasmvnc ne rend pas les contrôles clipboard, une session vnc/rdp/ssh les rend.

Effort : petit à moyen (~2-3h avec test), frontend uniquement, zéro changement de contrat backend.

---

## Tâche C : audio VNC réel via PulseAudio (indépendant de xrdp)

**Écart #3 (partie VNC).** `enable-audio` (`operator/pkg/params/params.go:116`, `Protocols: []string{"vnc"}`, `Tier: ui`) et `audio-servername` (`:121`, advanced) existent déjà au registre, avec une description honnête (« requires the image to run one »). Le catalogue interne (`waas-images/`) n'en fournit pas aujourd'hui (`waas-images/HARDENING.md:80-82`). **Contrairement au clipboard/audio RDP (tâche D), ceci ne dépend pas de xrdp/chansrv** : guacd sait streamer l'audio d'une session VNC directement depuis un serveur PulseAudio joignable en réseau (paramètre `audio-servername`), sans passer par xrdp. C'est une intégration standard de Guacamole, pas une extrapolation.

**À faire, dans `waas-images/base/ubuntu/`** :
1. Installer `pulseaudio` (+ `pulseaudio-utils` si besoin pour les tests smoke) dans `Dockerfile`, à côté du bloc `tigervnc-standalone-server` (ligne ~45-59). Reste dans le principe `--no-install-recommends` déjà en place.
2. Configurer PulseAudio pour tourner **en mode utilisateur, sans root, sans setuid** — c'est le mode natif de PulseAudio moderne, cohérent avec tout le reste de l'image (aucune régression attendue sur le hardening). Charge le module réseau nécessaire pour que guacd (qui tourne dans un autre pod) puisse se connecter en TCP au serveur PulseAudio de ce pod (`module-native-protocol-tcp`, avec une ACL/auth cohérente avec le modèle de menace déjà documenté — le trafic VNC/RDP est déjà en clair intra-cluster par design, `HARDENING.md` §Threat model — reste sur le même principe plutôt que d'inventer un mécanisme d'auth réseau à part).
3. Superviser PulseAudio via le même mécanisme que Xvnc/xrdp (`supervisord`, fragment rendu par `waas-entrypoint` dans `${RUNDIR}/supervisor.d/`, cf. le pattern existant pour xrdp autour de `waas-entrypoint:154`).
4. Ouvrir le port PulseAudio choisi dans `EXPOSE` (`Dockerfile:98`) et dans `waas-images/examples/networkpolicy-workspaces.yaml` (ajouter une entrée `port:` à côté de 5901/3389, même `from: guacd`).
5. Bump `version` dans `waas-images/base/ubuntu/manifest.yaml` (actuellement `1.2.0` — c'est un changement d'image, les tags sont immuables, la CI l'impose) et dans `waas-images/desktop/xfce/manifest.yaml` si cette image dérive du base modifié (vérifie `from: ubuntu-base-rdp`).
6. Mets à jour `waas-images/HARDENING.md` : retire ou reformule la ligne « Audio is not shipped » (section « Known, accepted gaps ») pour ne garder que ce qui reste vrai (RDP toujours sans audio, cf. tâche D), et ajoute une entrée dans « Enforced at runtime » décrivant le nouveau composant PulseAudio et sa portée réseau.
7. Étends `waas-images/ci/smoke_test.sh` (ou le mécanisme `smoke:` des manifests) si c'est réaliste de vérifier que le module PulseAudio écoute, sans exiger un vrai flux audio de bout en bout (out of scope : tester guacd lui-même).

**Contrainte non négociable** : ne touche à rien dans `operator/pkg/params/params.go` — le paramètre `enable-audio`/`audio-servername` existe déjà avec la bonne sémantique, cette tâche est une implémentation image, pas un changement de contrat. Ne t'approche pas de xrdp/RDP dans cette tâche — c'est le périmètre de la tâche D, avec une contrainte de sécurité différente (voir plus bas).

Effort : moyen (~1 jour), confiné à `waas-images/`.

---

## Tâche D : clipboard + audio RDP — investigation avant tout code

**Écarts #1 (matrice, ligne 1) et #3 (partie RDP).** Le clipboard RDP (`disable-copy`/`disable-paste` déjà au registre, gouvernés par la policy) et l'audio RDP (`disable-audio`, `enable-audio-input`, `params.go:160,165`) passent tous deux par **chansrv**, le canal de xrdp qui porte clipboard et audio. Aujourd'hui, `waas-images/base/ubuntu/Dockerfile:13-19` documente une décision délibérée : xrdp tourne **sans sesman/PAM**, en pont direct (`libvnc` backend) vers la session Xvnc déjà lancée — précisément pour rester « fully non-root (no PAM, no setuid) », cohérent avec le reste du hardening (`HARDENING.md` : zéro binaire setuid vérifié en CI, `find / -xdev -perm /6000`). Chansrv n'est démarré que par sesman à l'ouverture de session — sans sesman, pas de chansrv, donc pas de clipboard ni d'audio RDP. C'est la même contrainte architecturale pour les deux fonctionnalités : ne mène pas deux investigations séparées, une seule suffit.

**Ce qu'on te demande, dans cet ordre strict** :

1. **Investigue d'abord, sans écrire de code image.** Réponds concrètement à :
   - Est-il possible de faire tourner `xrdp-sesman` pour une session utilisateur **unique et fixe** (le conteneur n'a qu'un seul utilisateur, `WAAS_USER` UID 1000 — pas de bascule multi-utilisateur à gérer) sans que sesman ait besoin d'une authentification PAM réelle (mot de passe système, `/etc/shadow`) ? Le mot de passe de session existe déjà et est vérifié ailleurs (bridge `password=ask` documenté dans `waas-entrypoint`) — sesman n'a pas besoin de re-vérifier un mot de passe système, juste d'autoriser le seul utilisateur du conteneur à démarrer sa session existante. Un module PAM du type `pam_permit.so` (accepte toute tentative) est la piste évidente, mais vérifie qu'il ne réintroduit pas un vecteur d'authentification que rien d'autre ne garde (par exemple : est-ce que sesman, une fois PAM contourné, expose un moyen pour n'importe quel process du pod de se faire passer pour n'importe quel utilisateur ? Ici il n'y a qu'un utilisateur, donc l'impact devrait être nul, mais vérifie-le explicitement).
   - Les paquets `xrdp-sesman`/`xrdp` (et donc chansrv) embarquent-ils des binaires setuid/setgid sur Ubuntu 24.04 ? Si oui, est-ce strictement nécessaire pour le mode mono-utilisateur ciblé, ou est-ce lié à la bascule multi-utilisateur classique de xrdp (setuid pour changer d'UID à l'ouverture de session) que ce déploiement n'utilise pas ? Le smoke test CI (`ci/smoke_test.sh`) fait déjà `find / -xdev -perm /6000` — n'importe quelle régression sera détectée, mais fais cette vérification toi-même avant de proposer le changement.
   - Est-ce que sesman doit tourner en root pour son propre fonctionnement (indépendamment du besoin de changer d'UID), ou peut-il tourner sous l'UID 1000 comme tout le reste de l'image ?
2. **Si la réponse aux trois points est favorable** (mono-utilisateur possible sans PAM réel, pas de setuid nouveau nécessaire ou strippable sans casser le fonctionnement, pas besoin de root) : implémente-le. Ajoute sesman (mode minimal) + chansrv dans le variant `ubuntu-base-rdp` du Dockerfile, adapte `waas-entrypoint`/`xrdp.ini.tpl`/supervisord en conséquence (même pattern que le rendu de config existant pour xrdp), fais tourner le smoke test avec `--read-only --cap-drop ALL --security-opt no-new-privileges` pour confirmer qu'aucune régression de hardening n'apparaît, bump les versions de manifest concernées, et documente le nouveau composant dans `HARDENING.md` (retire la ligne « RDP path has no chansrv » de « Known, accepted gaps », ajoute la description du mécanisme sesman-lite dans « Enforced at runtime », avec le raisonnement de sécurité que tu as validé à l'étape 1). Ajoute un test de clipboard RDP dans `wwt/internal/guac/clipboard_test.go` si le protocole change de comportement observable par wwt.
3. **Si un des trois points est défavorable** (setuid nécessaire non strippable, besoin de root, ou risque d'authentification réel non maîtrisé) : **n'implémente rien**. Documente dans `waas-images/HARDENING.md` (section « Known, accepted gaps ») exactement ce que tu as trouvé et pourquoi c'est resté hors de portée sans régresser le hardening — remplace la ligne actuelle par une version qui montre que la question a été creusée, pas juste répétée. Ne touche à aucun Dockerfile dans ce cas.

**Ne mélange pas cette tâche avec la tâche C** : l'audio VNC (tâche C) ne dépend d'aucune de ces réponses et doit être livrée qu'importe l'issue de cette investigation.

Effort : variable — l'investigation seule est de l'ordre de quelques heures ; l'implémentation (si elle a lieu) est un chantier moyen à lourd (~1-2 jours), et touche la surface de sécurité la plus sensible de tout ce prompt — ne merge pas sans repasser par `/security-review` sur le diff final.

---

## Tâche E (la plus lourde) : vrai resize dynamique VNC/RDP, de bout en bout

**Écart #2, mais plus profond que ce que l'étude documente en surface.** `resize-method` (RDP, `params.go:145`) et le fait que rien ne redimensionne jamais la session en cours ne se règlent PAS en câblant `client.sendSize()` de `guacamole-common-js` : le script déjà présent dans l'image, `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-resize`, documente explicitement que **le pont xrdp-libvnc ne peut de toute façon pas répercuter un resize sur le Xvnc sous-jacent**, même si guacd recevait l'instruction. Envoyer `sendSize()` ferait donc un aller-retour pour rien. La seule voie qui fonctionne réellement aujourd'hui est ce script `waas-resize` exécuté **dans le pod**, qui pilote Xvnc via `xrandr` (RandR `SetDesktopSize`, supporté nativement par TigerVNC) — rien ne l'appelle depuis l'extérieur actuellement.

Le mécanisme à construire est donc **spécifique à WaaS, pas le mécanisme resize natif de guacd** : bureau redimensionné en exécutant une commande dans le pod, indépendamment de ce que RDP/VNC savent faire nativement. Ça s'applique à VNC ET RDP (les deux tournent sur le même Xvnc dans ce catalogue d'images) — pas à kasmvnc (déjà géré nativement côté client, `DesktopPane.tsx:156-163`) ni SSH (pas de bureau).

**Architecture recommandée** (vérifiée sur le code existant, pas une supposition) :

- **wwt n'a aujourd'hui aucun accès cluster** (pas de client Kubernetes, RBAC ou dépendance k8s dans `wwt/`) et son architecture actuelle (« parle uniquement à `shared/auth` + l'API interne ») ne doit pas en gagner un pour ce besoin — ce n'est pas sa responsabilité.
- **`api-server` a déjà un client Kubernetes et de l'accès RBAC** (`s.kube`, controller-runtime client, utilisé partout dans `WorkspaceService`, ex. `Reload` à `workspace_service.go:497`). C'est donc `api-server` qui doit exécuter la commande dans le pod, pas wwt.
- Le frontend parle déjà directement à `api-server` pour les actions de session (pause/wake/reload) sans passer par wwt — un nouvel endpoint public suit le même chemin, pas besoin d'inventer un canal via le tunnel guac (qui est un flux binaire opaque, mauvais candidat pour porter un signal hors-bande).

**À faire** :

1. **Frontend** : dans `DesktopPane.tsx`, le `ResizeObserver` existant (ligne ~292) ne fait aujourd'hui que rescaler le canvas en CSS. Ajoute, **débouncé** (le resize d'une fenêtre navigateur déclenche des dizaines d'événements par seconde — vise ~500ms après la dernière variation avant d'agir), un appel à un nouvel endpoint quand le protocole de la session est `vnc` ou `rdp` (pas kasmvnc, pas ssh — vérifie le protocole actif comme le fait déjà `SessionOverlay.tsx:105`).
2. **api-server, nouvel endpoint public** (à côté des autres actions de session existantes, ex. `Reload`/`Connect` dans `workspace_service.go` + le handler correspondant) : `POST /api/v1/workspaces/{id}/resize` (nom à adapter à la convention REST déjà en place — regarde comment `Reload` est exposé pour rester cohérent), body `{width, height}`.
   - Réutilise l'autorisation existante (`fetchByID(ctx, actor, id)`, même modèle que `Reload`) — pas de nouvelle logique d'auth.
   - Rejette explicitement les remote workspaces (`kind: "remote"`) : ils n'ont **aucun pod** (`model.go:82-86`, « no template, no operator lifecycle, no compute ») — un 400/409 explicite, pas un échec silencieux.
   - Valide strictement `width`/`height` côté serveur **avant** de construire quoi que ce soit (regex ou bornes numériques, ex. 100–7680 sur chaque axe) — défense en profondeur même si `remotecommand` n'invoque pas de shell (pas d'injection possible via les arguments, mais des valeurs absurdes peuvent quand même faire n'importe quoi à `xrandr` dans le pod).
   - Résous le pod cible par label selector dans le namespace du workspace (`waas.xorhub.io/workspace=<name>`, cf. `operator/pkg/metakeys` pour la clé exacte et `operator/pkg/naming` pour la convention de nommage) — il n'existe aujourd'hui aucune résolution de pod dans `api-server`, tu es le premier à en écrire une ; garde-la minimale (un seul pod attendu par workspace).
   - Exécute `waas-resize WIDTHxHEIGHT` via `client-go` (`kubernetes.Interface.CoreV1().Pods(ns).GetLogs`/`RESTClient().Post().Resource("pods").SubResource("exec")` + `remotecommand.NewSPDYExecutor`) — **commande fixe, un seul argument construit à partir des entiers déjà validés, jamais de shell** (pas de `sh -c`). `s.kube` (controller-runtime client) ne suffit pas pour l'exec — il te faut en plus un `kubernetes.Interface` (client-go classique) et un `*rest.Config` ; regarde comment `main.go` construit déjà la config in-cluster pour le reste et ajoute ce qu'il manque au constructeur du service, sans dupliquer la config de connexion.
   - Si le workspace n'est pas `Running`, retourne un conflit explicite (même pattern que `Reload`, `workspace_service.go:502-504`).
3. **RBAC** (`helm/waas/templates/api-server.yaml`) : le `ClusterRole` de l'api-server a aujourd'hui `pods: [get, list]` (autour de la ligne 73-75) — ajoute une entrée séparée `resources: [pods/exec]`, `verbs: [create]`. **Ne mélange pas ce verbe avec l'entrée `pods` existante** : `pods/exec` est un sous-droit à part entière et mérite sa propre ligne visible en review, pas noyé dans le `get, list` du reste.
4. **Tests** : côté Go, teste au moins la validation des bornes width/height et le rejet explicite des remote workspaces et des workspaces non-Running (fake client suffit, pas besoin d'un vrai exec en test — mock/interface l'exécuteur pour ne pas dépendre d'un vrai pod). Côté frontend, teste que le debounce n'envoie pas une requête par pixel et que kasmvnc/ssh n'appellent jamais l'endpoint.
5. **Documentation** : ajoute une note dans `docs/` (le fichier le plus proche est probablement `docs/diagnostics/` ou un nouveau doc court) expliquant que ce resize est un mécanisme WaaS maison (exec direct dans le pod), pas le resize natif de RDP/VNC — pour que le prochain lecteur ne cherche pas `sendSize()` dans le tunnel guac et ne perde pas de temps comme cette étude a failli le faire.

**Ce qui reste hors de portée de cette tâche** : rendre `resize-method` (le paramètre guacd RDP) lui-même fonctionnel au sens propre du terme — ce paramètre resterait inerte au sens guacd/RDP classique même après cette tâche, puisque le vrai mécanisme contourne complètement guacd. Si tu juges que ça rend le paramètre trompeur une fois ce mécanisme en place (l'utilisateur a un vrai resize, mais pas via le chemin que le paramètre `resize-method` est censé contrôler), signale-le en fin de tâche plutôt que de le corriger toi-même — c'est un arbitrage produit (faut-il garder/retirer/reformuler `resize-method` maintenant que le vrai mécanisme est ailleurs), pas un bug technique.

**Sécurité — ne merge pas sans repasser dessus** : cette tâche ajoute une nouvelle capacité d'exécution de commande dans un pod, déclenchable depuis le navigateur. Le rayon d'action est volontairement étroit (une commande fixe, deux entiers validés, un binaire qui valide lui-même son format en plus), mais `pods/exec` est un droit RBAC significatif — passe le diff final par `/security-review` avant de le considérer terminé, indépendamment des tests unitaires.

Effort : lourd (~2-3 jours), traverse frontend + api-server + Helm RBAC + doc — la tâche la plus transverse de ce prompt.

---

## Tâche F : formulaires kasmvnc vides — aucune action

**Écart #6.** `ForProtocol("kasmvnc")` ne retourne aucun paramètre par construction (`params.go:416-434`) : les tabs "paramètres protocole" du portail n'afficheront jamais rien pour kasmvnc, la vraie configuration passant par `kasmvncConfig` (YAML opaque admin-only). C'est un état jugé correct — **ne fais rien ici**, ne fabrique pas un message spécifique à un seul protocole pour combler un "écart" qui n'en est pas un. Mentionné uniquement pour que tu ne le redécouvres pas comme un bug en cours de route.

---

## Contraintes transverses

- `tsc -b` sans erreur, `strict: true`, zéro `any` sur tout changement frontend.
- `go build ./...` + tests Go existants verts sur tout changement backend.
- i18n : toute nouvelle chaîne passe par `frontend/src/i18n/locales/{en,fr}.json` — mais réfléchis avant d'en ajouter une pour un seul protocole (tâche B) : préfère l'absence de texte à un message qui n'existera que pour justifier son existence.
- N'étends pas le registre `pkg/params` dans ce prompt (aucune des tâches ne le demande — C/D changent l'image derrière un paramètre déjà existant, pas le paramètre lui-même).
- Chaque tâche est livrable seule — n'attends pas d'avoir fait A→E pour committer la première.
- Les tâches C, D et E touchent à la sécurité du catalogue d'images ou à une nouvelle capacité d'exécution cluster : ne les considère jamais "terminées" au sens de ce prompt tant que le smoke test hardening (`ci/smoke_test.sh`, `--read-only --cap-drop ALL --security-opt no-new-privileges`) et, pour E, une passe `/security-review`, n'ont pas confirmé l'absence de régression.

## Points ouverts (ton arbitrage, résumé)

- Tâche B : section clipboard absente vs. message explicatif pour une session kasmvnc.
- Tâche D : implémenter ou documenter-seulement selon ce que l'investigation sesman/PAM révèle réellement — ne force pas une implémentation si un des trois critères de sécurité n'est pas clairement rempli.
- Tâche E : que faire de `resize-method` une fois le vrai mécanisme en place ailleurs — signale, ne tranche pas silencieusement.
