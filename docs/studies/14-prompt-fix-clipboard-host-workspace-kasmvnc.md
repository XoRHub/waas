# Prompt Fable 5 — Fix (à confirmer) : copier-coller host↔workspace cassé côté kasmvnc, dans les deux sens

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

**Ce prompt est volontairement une investigation avant d'être un
correctif.** Le rapport dit : sur le protocole `kasmvnc`, aucun
copier-coller ne fonctionne entre le poste local (host) et le
workspace, **dans les deux sens** (host→workspace ET workspace→host).
Il existe plusieurs causes plausibles, de nature très différente (bug
de code WaaS, config/policy manquante en environnement de test,
limitation propre au client KasmVNC lui-même hors de portée de ce
repo) — **ne code pas de correctif avant d'avoir isolé laquelle
s'applique réellement**. Un fix qui masquerait un problème de policy
mal configurée, par exemple, serait pire que pas de fix du tout.

## Contexte du repo : ce qui gère le clipboard kasmvnc aujourd'hui

Le protocole `kasmvnc` ne passe PAS par le tunnel guacd : `DesktopPane.tsx`
détecte `result.protocol === 'kasmvnc'` (`DesktopPane.tsx:~150-165`) et
monte un `<iframe>` pointant sur le client web KasmWeb, reverse-proxié
par wwt sous `/kasm/{session}/...` (même origine que le reste de
l'app). **Cet iframe n'a aujourd'hui aucun attribut `allow`**
(`DesktopPane.tsx:342-350`) :

```tsx
<iframe
  src={kasmUrl}
  title={t('connect.desktopFrame', 'Remote desktop')}
  className={state === 'connected' ? 'h-full w-full border-0' : 'hidden'}
  onLoad={...}
/>
```

Tout le reste du clipboard kasmvnc (lecture/écriture du presse-papiers
système, UI de copier-coller) vit **dans le client KasmWeb lui-même**
(code tiers servi par le conteneur KasmVNC, absent de ce repo) — WaaS
ne fait qu'agir sur deux leviers, tous deux DANS ce repo :

1. **Le proxy** (`wwt`) qui reverse-proxie l'iframe — peut potentiellement
   casser des en-têtes nécessaires (voir §1 ci-dessous).
2. **La politique DLP clipboard**, dérivée de `WorkspacePolicy.Clipboard`
   et stampée dans `~/.vnc/kasmvnc.yaml` par le contrôleur :
   - `operator/pkg/kasmcfg/kasmcfg.go:20-24` — les 3 clés gérées :
     `data_loss_prevention.clipboard.server_to_client.enabled` (sens
     workspace→host, "copier depuis le workspace"),
     `data_loss_prevention.clipboard.client_to_server.enabled` (sens
     host→workspace, "coller dans le workspace"), et
     `runtime_configuration.allow_client_to_override_kasm_server_settings`
     forcé à `false`.
   - `operator/internal/controller/kasm_config.go:80-104` (`applyClipboardPolicy`)
     stampe ces valeurs à partir de `kasmClipboardGrant` (`kasm_config.go:70-79`),
     lui-même basé sur `policy.ClipboardOf(pol)` (`operator/pkg/policy/policy.go:161-171`).
   - **Point critique** : `policy.ClipboardOf` renvoie `(true, true)`
     (tout autorisé) si une policy résout mais ne définit pas
     `Clipboard`, **mais renvoie `(false, false)` si AUCUNE policy ne
     résout pour l'identité du workspace** (`kasmClipboardGrant`,
     commentaire « Resolution failure fails closed: no policy match
     means clipboard off »). Un environnement de test sans policy
     par défaut couvrant l'utilisateur testé désactiverait donc les
     DEUX sens du clipboard kasmvnc **par conception**, pas par bug.

## Ce qu'il faut livrer

### 1. Isoler la cause avant tout correctif

Sur l'environnement où le bug est observé (dev k3d ou autre), pour LE
workspace kasmvnc concerné :

1. Lis la ConfigMap effective du workspace (endpoint mentionné en
   mémoire projet `/kasmvnc-config`, ou directement
   `kubectl get configmap <workload-name> -o yaml` dans le namespace du
   workspace) et vérifie l'état réel de
   `data_loss_prevention.clipboard.server_to_client.enabled` et
   `.client_to_server.enabled`.
   - **Si l'une ou les deux valent `false`** : remonte jusqu'à la
     `WorkspacePolicy` qui s'applique (ou l'absence de résolution,
     fail-closed) via `policy.Resolve` (`operator/pkg/policy/policy.go`).
     Si c'est un manque de policy par défaut dans l'environnement de
     test, **ce n'est pas un bug de ce repo** — documente-le comme tel
     et arrête-toi là pour ce sous-cas (le fail-closed est un choix de
     sécurité déjà décidé, ne le relitige pas ici) ; si en revanche tu
     identifies un cas où la policy DEVRAIT résoudre et ne résout pas
     (bug de matching), c'est un bug légitime — corrige-le et
     documente le cas exact qui le déclenche.
2. **Si les deux clés valent bien `true`** (clipboard autorisé dans les
   deux sens par la policy) et que le copier-coller échoue quand même :
   la cause est ailleurs, dans le chemin navigateur ↔ iframe ↔ client
   KasmWeb. Regarde alors les deux pistes suivantes.

### 2. Piste concrète : l'iframe n'a pas la permission clipboard

L'iframe KasmWeb (`DesktopPane.tsx:342-350`) n'a pas d'attribut `allow`.
Sans `allow="clipboard-read; clipboard-write"`, le navigateur peut
refuser au contenu de l'iframe l'accès à `navigator.clipboard` même
quand la page parente y a elle-même accès — c'est un comportement de
Permissions Policy indépendant du sens (host→workspace ET
workspace→host), ce qui correspondrait exactement au rapport (« les
deux sens, tout protocole kasmvnc confondu »).

- Ajoute `allow="clipboard-read; clipboard-write"` sur l'`<iframe>` et
  **teste en conditions réelles** (Chromium + HTTPS) si ça suffit à
  débloquer un sens, les deux, ou aucun.
- Si ça règle le problème, c'est tout le correctif nécessaire pour ce
  prompt — ne complique pas au-delà.
- Si ça ne suffit pas, documente ce que tu observes (quel sens marche,
  lequel ne marche pas, message d'erreur console le cas échéant côté
  client KasmWeb) : la suite dépend du client KasmWeb lui-même, hors de
  ce repo.

### 3. Piste à ne PAS creuser au-delà du raisonnable : le client KasmWeb lui-même

Le code du client web KasmVNC (dans l'image du conteneur, pas dans ce
repo) gère lui-même sa synchronisation clipboard. Si §1 et §2
n'expliquent pas le symptôme, WaaS n'a plus de levier direct — ne
te lance pas dans du reverse-engineering du client KasmWeb ou dans un
correctif qui contournerait son fonctionnement interne (ex. injecter du
JS dans l'iframe, du bricolage cross-origin). Documente le constat
(cause probable hors du repo, ex. limitation/bug upstream KasmVNC) et
propose une escalade (ticket upstream, ou mitigation via la doc
utilisateur) plutôt qu'un correctif de contournement fragile.

## Contraintes

- Ne touche pas au chemin guacd (prompt séparé,
  `docs/studies/13-prompt-fix-clipboard-host-workspace-guacd.md`) — ce
  prompt-ci ne concerne que `kasmvnc`.
- Ne relitige pas le comportement fail-closed de `policy.ClipboardOf`
  (`operator/pkg/policy/policy.go:161-171`) — c'est un choix de
  sécurité déjà décidé (Feature 11,
  `docs/studies/08-prompt-feature11-kasmvnc-governance-gap.md`), pas un
  bug.
- Si tu ajoutes `allow` à l'iframe, vérifie que ça ne relâche pas plus
  de permissions que nécessaire (limite-toi à `clipboard-read` et
  `clipboard-write`, n'ajoute pas d'autres features par précaution).

## Tests

- Vérification manuelle en navigateur (Chromium, HTTPS ou localhost),
  sur un template kasmvnc dont la policy autorise explicitement les
  deux sens : copier une appli hors navigateur → coller dans le
  workspace kasmvnc, puis l'inverse (copier dans le workspace → coller
  hors du navigateur). Documente précisément quel sens fonctionne après
  ton changement.
- Non-régression : un template/policy qui interdit un sens (ou les
  deux) continue de le bloquer effectivement dans le conteneur (le
  fail-closed et les DLP keys stampées restent inchangés si tu ne
  touches que l'iframe).
- Pas de test unitaire attendu pour la partie iframe/permissions
  (comportement de navigateur réel, pas simulable en jsdom) ; si tu
  corriges un bug de résolution de policy (§1), ajoute le test Go
  correspondant dans `operator/internal/controller` ou
  `operator/pkg/policy`.

## Points ouverts (ton arbitrage)

- Le sous-cas qui s'applique réellement (policy manquante en
  environnement de test / permission iframe / limitation client
  KasmWeb) ne peut être tranché qu'en reproduisant — n'anticipe pas
  lequel avant de l'avoir observé.
- Si le fix se limite à l'attribut `allow` de l'iframe, pas de nouvelle
  section à ajouter à l'overlay ; si tu identifies qu'un fallback
  manuel façon `SessionOverlay.tsx` (cf. le chemin guacd) aurait du
  sens pour kasmvnc, note-le comme piste plutôt que de l'implémenter
  sans qu'on te l'ait demandé — l'intégration avec un client KasmWeb
  embarqué en iframe n'est pas triviale (WaaS ne contrôle pas son DOM
  interne).

---

## Résultat de l'investigation (2026-07-10, Fable 5)

**Cause racine identifiée et corrigée : le client KasmWeb désactive lui-même
son clipboard quand il tourne dans une iframe.** Dans le bundle UI des images
kasmweb 1.19, `window.self !== window.top` sans `show_control_bar` en query
param fait initialiser `clipboard_up`, `clipboard_down` et
`clipboard_seamless` à `false` — copier-coller mort dans les deux sens, quel
que soit l'état de la policy. Ces settings sont lus en priorité depuis l'URL
de `vnc.html`.

**Fix (une seule modification, `frontend/src/components/DesktopPane.tsx`)** :
ajout de `clipboard_up=1`, `clipboard_down=1`, `clipboard_seamless=1` aux
query params de l'iframe kasmvnc.

**Hypothèses du prompt, tranchées en reproduction (dev k3d, Chromium/Playwright)** :

1. **Policy DLP (§1) : hors de cause.** ConfigMap effective du workspace de
   test vérifiée : les deux clés `enabled: true` (policy `admins` résout,
   `Clipboard` non défini → `(true, true)`).
2. **Attribut `allow` sur l'iframe (§2) : non nécessaire, non ajouté.**
   L'iframe est same-origin (`/kasm/...` relatif) : la Permissions Policy
   du parent est héritée — mesuré `allowsFeature('clipboard-read'/'write')
   === true` dans l'iframe sans attribut.
3. Le proxy wwt ne touche ni Permissions-Policy ni les en-têtes de réponse.

**Validation e2e (Chromium headless, origine localhost = contexte sécurisé,
params injectés avant la première connexion)** :

- host→workspace : `navigator.clipboard.writeText` côté host → texte lu par
  `xclip -o` dans le conteneur. **PASS.** (Le client ne relit le presse-papiers
  local qu'après un blur/focus fenêtre suivi d'un événement utilisateur —
  c'est le geste naturel « copier ailleurs, revenir, cliquer ».)
- workspace→host : `xclip -i` dans le conteneur → `navigator.clipboard.readText`
  côté host. **PASS.**
- **Non-régression DLP** : policy `admins` passée à deny/deny → après reload
  (le drift policy ne s'applique qu'à une frontière resume/reload, by design),
  les deux sens sont bloqués malgré les params client. La DLP conteneur reste
  l'autorité (`-IgnoreClientSettingsKasm 1` + `allow_client_to_override…: false`).

**Limites documentées (hors de portée du fix)** :

- **Contexte sécurisé requis** : sur `http://waas.127.0.0.1.nip.io:8080`
  (dev, HTTP non-localhost) `navigator.clipboard` n'existe pas du tout
  (`isSecureContext: false`, parent ET iframe) — aucun clipboard possible,
  fix ou pas. En prod HTTPS ou en dev via `localhost`, OK. C'est une
  limitation d'environnement, pas un bug du repo.
- **Firefox/Safari** : le client KasmWeb force `clipboard_seamless` off
  (détection UA upstream) ; la control bar Kasm étant masquée, il n'y a pas
  non plus d'UI manuelle. Piste (non implémentée, à arbitrer) : fallback
  manuel façon SessionOverlay guacd, ou exposer la control bar Kasm.
- Observation annexe : le contrôleur ne watch pas les WorkspacePolicy — un
  changement de policy n'est re-stampé dans la ConfigMap qu'à la prochaine
  réconciliation du workspace, et le pod ne roule qu'au resume/reload
  (choix de stabilité de session documenté dans `workload.go`).
