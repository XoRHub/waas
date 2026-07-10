# Prompt Fable 5 — Étude 1 : tableau des features supportées par protocole

Colle ce document tel quel comme prompt d'étude (**aucune implémentation attendue**, uniquement un livrable markdown). Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Objectif

Produire un état des lieux sourcé (fichier + ligne, comme le fait déjà `docs/studies/audit-2026-07.md`, à relire pour le ton et le niveau de rigueur attendu — pas de chiffre estimé, tout est vérifié dans le code) : **un tableau croisé protocole × fonctionnalité**, disant pour chaque case si c'est supporté, partiellement supporté, ou pas supporté, avec la source exacte de l'affirmation. Le format cible est proche de `docs/templates-and-protocols.md` (déjà existant, à lire en premier — ne redis pas ce qu'il documente déjà, référence-le) mais organisé en tableau de fonctionnalités transverses plutôt qu'en doc narrative par sujet.

## Protocoles à couvrir

Quatre chemins de connexion existent aujourd'hui, à traiter comme quatre colonnes distinctes (ils n'ont pas les mêmes mécanismes sous le capot) :
- **VNC** — brokered par guacd.
- **RDP** — brokered par guacd.
- **SSH** — brokered par guacd (terminal, pas un bureau graphique — certaines lignes du tableau ne s'appliquent pas, marque "N/A" explicitement plutôt que "non supporté").
- **KasmVNC** — chemin **différent** : pas de guacd, reverse-proxy HTTP brut fait par `wwt/internal/kasm` directement vers le serveur KasmVNC embarqué dans l'image (`docs/studies/kasm-images-feasibility.md` pour le contexte de la décision, section "Kasm phase 1-4" du journal projet si accessible). Les paramètres guacd n'existent pas sur ce chemin — vérifie ça dans `operator/pkg/params/params.go` (`Protocols()` fin de fichier : `kasmvnc` est listé mais **aucune entrée du registre n'a `kasmvnc` dans `Protocols`** → tout override y est rejeté fail-closed).

Traite aussi séparément, en note ou colonne annexe si pertinent, les **workspaces distants** (`RemoteWorkspace`, `model.RemoteProtocol`) — ce sont des machines hors cluster connectées via le même wwt mais avec un modèle de policy différent (pas de template/CRD Workspace, gouvernance plus légère) ; certaines fonctionnalités du tableau peuvent différer entre un workspace in-cluster et une remote machine sur le même protocole.

## Fonctionnalités à évaluer (base de départ — complète si tu en trouves d'autres pendant la lecture du code)

Pour chacune, la source de vérité principale est `operator/pkg/params/params.go` (le registre — chaque `Param` a `Protocols`, `Tier`, `Live`, `Default`) et `docs/guacd-parameters.md` (généré depuis le registre par `make docs-params`, `operator/cmd/paramsdoc/main.go` — vérifie qu'il est à jour, régénère-le si besoin plutôt que de lire un fichier périmé). Ne te contente pas de recopier le registre : dis pour chaque feature si elle est **exposée en UI** (`Tier: ui`), **accessible seulement en CR/YAML** (`Tier: advanced`), ou **totalement bloquée côté plateforme** (`Tier: platform`, avec la raison écrite dans `Description`), et si elle est **applicable à chaud sans reconnexion** (`Live: true`).

1. **Son / audio** — lecture (`enable-audio` VNC, `disable-audio`+défaut actif RDP) et micro/entrée (`enable-audio-input` RDP). SSH = N/A. KasmVNC = à vérifier dans la doc de faisabilité (`docs/studies/kasm-images-feasibility.md` mentionne "audio = plateforme Kasm uniquement, pas notre chemin standalone" — confirme si c'est toujours l'état actuel).
2. **Copier/coller (clipboard)** — `disable-copy`/`disable-paste` (VNC/RDP/SSH, `Live: true`, appliqué par le `ClipboardFilter` de wwt). Lis `docs/clipboard.md` en entier, il a déjà une matrice partielle (RDP limité par l'image xrdp-libvnc, Firefox sans `readText`) — vérifie qu'elle est toujours exacte et intègre-la. KasmVNC : le clipboard n'est **pas gouverné** sur ce chemin (mentionné comme décision v1 assumée dans le journal projet) — vérifie l'état actuel dans `wwt/internal/kasm` (y a-t-il un `ClipboardFilter` équivalent, ou vraiment rien ?).
3. **Volume partagé / home persistant** — le PVC home est toujours monté (`docs/volumes.md`), modèle de rétention par labels PVC (pas de table DB). Vérifie si un vrai "volume partagé entre plusieurs workspaces" existe (probablement non — chaque workspace a SON home ; s'il n'existe qu'un mécanisme d'adoption d'un volume existant à la création, dis-le clairement, ce n'est pas la même chose qu'un partage concurrent).
4. **Transfert de fichiers** — `enable-sftp` (VNC/RDP/SSH) et `enable-drive` (RDP) sont `Tier: platform`, explicitement bloqués avec la raison "jusqu'à ce que la feature file-transfer ait sa propre policy gate" — **non supporté aujourd'hui**, dis-le sans ambiguïté plutôt que de lister le paramètre comme "disponible en advanced".
5. **Enregistrement de session** — `recording-path`/`recording-name`/`create-recording-path`/`typescript-path` : tous `Tier: platform`, même statut que le point 4 (non supporté, en attente d'une policy gate dédiée).
6. **Disposition clavier** — `server-layout` (RDP, `Tier: ui`, enum de ~24 layouts + auto-détection depuis la locale navigateur, `docs/templates-and-protocols.md` section clavier). VNC/SSH : vérifie s'il existe un équivalent (a priori non — le clavier VNC suit le layout du serveur X, pas de négociation guacd).
7. **Redimensionnement / resize** — `resize-method` (RDP, `Tier: ui`, live-update vs reconnexion). VNC : vérifie s'il existe un paramètre équivalent dans le registre (a priori la résolution VNC est fixée côté serveur Xvnc, pas de resize dynamique par guacd). KasmVNC : `docs/studies/kasm-images-feasibility.md` mentionne un resize dynamique natif client (`AcceptSetDesktopSize`) — confirme si le chemin wwt actuel l'exploite déjà.
8. **Multi-écran** — mentionné comme capacité standalone de KasmVNC dans l'étude de faisabilité ; vérifie si `frontend/src/components/DesktopPane.tsx` ou l'iframe KasmVNC (`wwt/internal/kasm`) l'exposent réellement aujourd'hui, ou si c'est resté théorique.
9. **Qualité d'image / couleur** — `color-depth` (VNC+RDP), `force-lossless`/`swap-red-blue` (VNC), `cursor` local/remote (VNC).
10. **Paramètres applicables à chaud (`Live: true`)** — fais une colonne/liste séparée : à date, seuls `disable-copy`/`disable-paste` le sont dans le registre (`grep Live: true` dans `params.go`) ; vérifie qu'aucun autre n'a été ajouté depuis.
11. **Overrides niveau workspace après création** — si tu réalises cette étude après que la Feature 1 (`docs/studies/prompt-feature1-workspace-runtime-config.md`) a été implémentée, mentionne son existence et son statut ; sinon note explicitement que ce n'est **pas encore possible aujourd'hui** (seule la création initiale permet de fixer env/resources/placement).

## Format attendu

Un unique fichier markdown, dans le style de `docs/studies/audit-2026-07.md` :
- Un tableau principal `Fonctionnalité × {VNC, RDP, SSH, KasmVNC}` avec des valeurs courtes (`✅ ui`, `⚙️ advanced (CR/YAML only)`, `🚫 platform-bloqué`, `N/A`, `❓ à vérifier live`) et une note de bas de tableau par ligne renvoyant à la source (fichier:ligne).
- Une section par fonctionnalité un peu plus longue quand le sujet a une histoire (clipboard, file-transfer/recording bloqués intentionnellement, kasmvnc hors registre) — c'est là que `docs/clipboard.md`, `docs/volumes.md`, `docs/templates-and-protocols.md` et l'étude kasm doivent être cités et réconciliés, pas juste linkés.
- Une section finale "Écarts vs. ce que l'UI laisse penser" si tu trouves des endroits où le frontend affiche quelque chose (ex. un paramètre en formulaire) qui ne fait en réalité rien pour un protocole donné, ou l'inverse (une capacité réelle non exposée en UI).

## Contraintes

- **Aucune implémentation.** Ce document est un audit, pas un ticket de dev — n'ajoute ni ne modifie de code.
- Chaque affirmation doit être sourcée (fichier + ligne, ou nom de doc existant) — pas d'estimation, pas de "probablement". Si tu ne peux pas vérifier un point avec certitude (ex. comportement runtime non testable sans cluster), dis-le explicitement plutôt que de deviner.
- Régénère `docs/guacd-parameters.md` si tu soupçonnes qu'il a dérivé du registre (`make docs-params`) avant de t'appuyer dessus.
- Dépose le livrable dans `docs/studies/` (nom suggéré : `docs/studies/protocol-feature-matrix-<date>.md`) — ne le committe pas toi-même sauf si on te le demande explicitement.
