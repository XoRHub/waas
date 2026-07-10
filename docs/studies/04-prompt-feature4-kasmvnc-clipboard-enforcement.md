# Prompt Fable 5 — Feature 4 : faire fonctionner (et enforcer honnêtement) le clipboard sur les sessions kasmvnc

> **Périmé, remplacé par `08-prompt-feature11-kasmvnc-governance-gap.md`
> (2026-07-10).** Seule la partie affichage (§C ci-dessous) a été
> livrée (commit `87464e8a7865`) ; l'enforcement réel (§A/§B) ne l'a
> jamais été et a été absorbé, avec un périmètre élargi (API kasmvnc
> exposée sans filtrage), par la Feature 11. Ne reprends pas ce
> document — utilise le nouveau.

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

WaaS pilote des sessions VNC/RDP/SSH via guacd, et kasmvnc via un chemin séparé : `wwt/internal/kasm` fait un reverse-proxy HTTP/WebSocket brut vers le pod KasmVNC (`kasm.go:167-202`, `httputil.ReverseProxy`), **sans jamais passer par guacd**. `wwt/internal/proxy/proxy.go:96-102` refuse même explicitement kasmvnc sur `/ws` ("kasmvnc sessions connect through /kasm, not /ws"). Le `ClipboardFilter` de guacd (`guac.NewClipboardFilter`, instancié uniquement dans le pipe guacd, `proxy.go:142`) n'a donc **aucune prise** sur kasmvnc — c'est structurel, pas un oubli.

## Ce qui existe déjà (à connaître avant de coder)

**Le grant clipboard est protocol-agnostic aujourd'hui, partout dans la chaîne :**

- CRD : `WorkspacePolicySpec.Clipboard` (`operator/api/v1alpha1/workspacepolicy_types.go:63-68`) → `ClipboardPolicy{CopyFromWorkspace, PasteToWorkspace *bool}` (lignes 97-108). Aucun champ protocole.
- Résolution : `policy.ClipboardOf(pol)` (`operator/pkg/policy/policy.go:161-174`) retourne `(true,true)` si `pol.Spec.Clipboard` est nil, sinon les deux bools — ne prend aucun paramètre de protocole.
- Consommation : `resolveClipboardGrant` (`api-server/internal/service/workspace_service.go:629-646`), appelée depuis `workspace_service.go:607` et `remote_workspace_service.go:437` — **aucun des deux appels ne branche sur le protocole de la session**.
- Exposition au frontend : `SessionCapabilities{ClipboardCopy, ClipboardPaste bool}` (`api-server/internal/model/model.go:466-469`), peuplé identiquement pour tous les protocoles (`workspace_service.go:597-598`, `remote_workspace_service.go:450-451`).

**Côté registre guacd**, `disable-copy`/`disable-paste` (`operator/pkg/params/params.go:83-92`) sont scopés `Protocols: []string{"vnc","rdp","ssh"}` — **kasmvnc en est explicitement exclu**, alors que `Protocols()` (ligne 447) liste bien `vnc, rdp, ssh, kasmvnc` comme protocole reconnu. Cohérent avec le fait que ces params n'ont de sens que dans le tunnel guacd.

**Commit `87464e8a7865` (déjà livré)** a rendu l'UI honnête côté affichage seulement : `frontend/src/components/SessionOverlay.tsx` affiche désormais, pour `protocol === 'kasmvnc'`, une phrase statique explicative (`overlay.clipboardKasmvnc`) à la place des toggles live copier/coller — parce que `tunnelRef` est `null` et `sendClipboardRef` un no-op sur ce chemin. Le message du commit dit explicitement : le backend continue d'émettre `capabilities.clipboardCopy/Paste` quel que soit le protocole — **c'était un fix d'affichage uniquement, aucun changement d'enforcement.**

**KasmVNC a son propre mécanisme de clipboard**, complètement en dehors de cette codebase : `docs/studies/kasm-images-feasibility.md:41` note que le client web KasmVNC (mode standalone) a un clipboard navigateur-natif intégré, indépendant de guacd. La décision v1 documentée (même fichier, ligne 5 et 84) est "clipboard non gouverné acceptable en v1 avec affichage honnête" — cette feature lève cette limitation. Il n'y a **aucune configuration KasmVNC locale dans ce repo** : les images `kasmweb/*` sont tirées telles quelles depuis Docker Hub (pas de Dockerfile/`kasmvnc.yaml`/supervisord dans `waas-images/` pour kasmvnc) — donc aucun point d'accroche existant pour piloter le clipboard natif de KasmVNC depuis WaaS.

## Ce qu'il faut livrer

### A. Rendre le grant honnête de bout en bout (obligatoire, indépendant du reste)

`resolveClipboardGrant` doit devenir protocole-conscient : pour kasmvnc, tant que rien n'enforce réellement le grant (avant que B soit livré), il ne doit **pas** rapporter `clipboardCopy`/`clipboardPaste` comme un droit accordé par la policy WaaS — soit en le forçant à `false`, soit en ajoutant un état explicite du style "non gouverné" distinct de `true`/`false`, à faire remonter dans `SessionCapabilities` pour que le frontend puisse afficher un état réellement fidèle (et pas juste un texte statique déconnecté de ce que dit `capabilities`).

### B. Faire fonctionner l'enforcement réel pour kasmvnc

**Ceci nécessite une recherche sur KasmVNC lui-même, qui n'est pas documentée dans ce repo — ne l'invente pas, vérifie la version/doc réelle de l'image `kasmweb/*` utilisée.** KasmVNC (upstream, projet Kasm Technologies) expose généralement le contrôle du clipboard via :
- des variables d'environnement au démarrage du conteneur (à vérifier sur l'image réellement utilisée — cherche `KASM_` ou équivalent dans les logs/doc de l'image, ou dans le binaire `kasmvncserver`/`kasmvnc.yaml` s'il est extrait du conteneur), et/ou
- des paramètres de l'URL du client web KasmVNC (query string sur la page servie), et/ou
- un fichier de config `kasmvnc.yaml` monté dans le conteneur.

Investigue laquelle de ces voies est réellement disponible sur l'image en usage (`docker run` local + inspection, ou `docker exec` + lecture des fichiers de config présents), puis câble-la : quand `policy.ClipboardOf` renvoie `(false, false)` pour un workspace kasmvnc, la configuration effective du conteneur (via env var injectée par l'opérateur au moment du provisioning, ou via un fichier de config généré) doit désactiver réellement le copier/coller du client KasmVNC — pas juste cacher un bouton côté WaaS.

### C. UI fidèle

Une fois B en place, `SessionOverlay.tsx` doit refléter l'état réel (autorisé/non-autorisé) plutôt que la phrase statique actuelle — remplace le texte fixe de 87464e8a7865 par un affichage qui lit `capabilities.clipboardCopy`/`clipboardPaste` comme pour les autres protocoles, avec éventuellement une mention "clipboard KasmVNC natif" pour distinguer du mécanisme guacd si utile à l'utilisateur.

## Contraintes à respecter

- Ne touche pas au chemin guacd (`disable-copy`/`disable-paste`, `ClipboardFilter`) — cette feature est strictement le chemin kasmvnc.
- Si le mécanisme d'enforcement KasmVNC nécessite un changement au provisioning du pod (env var, ConfigMap montée), passe par l'opérateur (`operator/internal/controller/workload.go` / `kasm_credentials.go`) de la même manière que les autres injections de config kasm existantes — pas de logique ad hoc côté `wwt`.
- Tests : couverture Go sur la nouvelle branche protocole de `resolveClipboardGrant` (kasmvnc vs autres), et sur le câblage opérateur si tu ajoutes une env var/ConfigMap. Test vitest sur le nouveau rendu `SessionOverlay.tsx` pour kasmvnc (au moins : grant vrai → toggles/infos affichés, grant faux → message honnête).
- i18n : toute nouvelle chaîne passe par `frontend/src/i18n/locales/{en,fr}.json`.
- Mets à jour `docs/studies/kasm-images-feasibility.md` : la décision "clipboard non gouverné acceptable en v1" devient caduque une fois cette feature livrée — documente le nouveau mécanisme à la place, ne laisse pas les deux versions coexister dans le même fichier.

## Points ouverts (ton arbitrage)

- Mécanisme exact d'enforcement côté KasmVNC (env var / config file / query param) — dépend de l'image réellement utilisée, à déterminer par investigation avant de coder, pas par supposition.
- Représentation du "non gouverné" en transition (avant que B soit livré, si tu livres A et B dans des changements séparés) — un état à trois valeurs (`granted`/`denied`/`ungoverned`) est plus honnête qu'un simple bool, mais change le contrat `SessionCapabilities` ; documente le choix si tu introduis ce triple état.
- Si l'image KasmVNC en usage ne permet aucun contrôle programmatique du clipboard (certaines images grand public ne l'exposent pas), documente-le clairement comme limitation technique amont plutôt que de forcer un enforcement partiel qui donnerait une fausse impression de sécurité.
