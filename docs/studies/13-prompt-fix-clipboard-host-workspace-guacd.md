# Prompt Fable 5 — Fix : clipboard host↔workspace mort en dev (guacd) — HTTPS requis sur l'env de dev

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

Le pendant kasmvnc de ce bug a déjà été traité (commit `afc081da`,
prompt `docs/studies/14-prompt-fix-clipboard-host-workspace-kasmvnc.md`) :
la conclusion là-bas était la même — le clipboard seamless exige un
**contexte sécurisé** — et le fix kasmvnc ne peut donc lui non plus être
vérifié en dev tant que l'env de dev reste en HTTP pur. Le HTTPS de dev
livré ici sert les deux protocoles.

## Symptôme (corrigé — l'ancienne version de ce prompt était fausse)

- **À l'intérieur d'un workspace**, le copier-coller fonctionne
  normalement (copier dans une appli du bureau distant, coller dans une
  autre appli du même bureau).
- **Entre le poste local (host) et un workspace**, le clipboard seamless
  ne fonctionne dans **aucun des deux sens** : ni copier sur le host →
  Ctrl+V dans le workspace, ni copier dans le workspace → coller sur le
  host.
- Le fallback manuel de l'overlay (`SessionOverlay.tsx`, section
  « clipboardManual », les deux `<textarea>`) fonctionne — le bug est
  dégradant, pas bloquant.

## Cause racine (vérifiée sur code — à confirmer en navigateur)

L'environnement de dev est servi en **HTTP pur** sur
`http://waas.127.0.0.1.nip.io:8080` (cf. `hack/dev/values-dev.yaml` :
`ingress.tls.enabled: false` ; `hack/dev/k3d-config.yaml` : seul le port
80 du loadbalancer est mappé). Or `waas.127.0.0.1.nip.io` n'est **pas**
un contexte sécurisé : les navigateurs jugent l'origine sur son nom
(`localhost`, `*.localhost`, IP littérale `127.0.0.1`), pas sur ce que
le DNS résout — un sous-domaine nip.io en http ne compte pas, même s'il
pointe sur la loopback (`window.isSecureContext === false`).

Conséquence : `navigator.clipboard` **n'existe pas du tout** sur cette
origine, et tout le pont clipboard du chemin guacd se désactive par ses
propres gardes — comportement déjà documenté honnêtement dans
`frontend/src/lib/clipboard.ts` (en-tête L9-15) :

1. **remote→host mort** : `client.onclipboard`
   (`frontend/src/components/DesktopPane.tsx:210-228`) ne tente
   `writeText` que si `hasClipboardApi()` (L224) — faux ici.
2. **host→remote seamless mort** : `syncFromSystem()` (L254-260,
   déclenché sur `focus` et à la connexion) sort immédiatement sur
   `canReadSystemClipboard()` — faux ici.
3. **host→remote via l'event `paste` (L245-249) mort aussi**, mais pour
   une raison indépendante du HTTPS : `Guacamole.Keyboard(container)`
   (L298+) attache ses listeners en capture sur le même `container` et
   appelle `e.preventDefault()` sur toute touche interprétée (Ctrl et V
   compris — `onkeydown` renvoie `undefined` car `sendKeyEvent` ne
   retourne rien, traité comme « bloquer le défaut »). Un keydown dont
   le défaut est empêché ne déclenche jamais la commande d'édition
   native → l'event DOM `paste` ne se produit pas pendant un Ctrl+V réel
   dans le pane.
4. **Pourquoi l'intérieur du workspace marche** : ce chemin ne passe
   jamais par le navigateur — les frappes sont relayées au bureau
   distant qui gère son propre presse-papiers. Cohérent avec le
   symptôme.

Le point 3 explique pourquoi même le « filet de sécurité » censé marcher
sans HTTPS ne sauve pas la mise. Mais la cause dominante et le
prérequis de tout le reste, c'est l'absence de contexte sécurisé : sans
elle, rien n'est même testable.

## Ce qu'il faut livrer

### 1. HTTPS sur l'environnement de dev (livrable principal)

Objectif : `https://waas.127.0.0.1.nip.io:8443` fonctionnel avec un
certificat auto-signé — l'avertissement navigateur est accepté
manuellement une fois, c'est assumé. cert-manager est **déjà installé**
par `make dev-up` et le chart supporte déjà le TLS d'ingress
(`helm/waas/templates/ingress.yaml:9-27` : annotation
`cert-manager.io/issuer` ou `cluster-issuer` selon `issuerRef.kind`,
section `tls` avec `secretName: {{ .Release.Name }}-public-tls`).

- **`hack/dev/k3d-config.yaml`** : ajouter le mapping
  `- port: "8443:443"` avec `nodeFilters: ["loadbalancer"]`. Pour les
  clusters existants (le fichier n'est lu qu'à la création) : documente
  `k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"` comme
  alternative à `make dev-reset` (le edit recrée le conteneur
  loadbalancer seulement, sans perdre le cluster).
- **`hack/dev/values-dev.yaml`** : passer `ingress.tls.enabled: true`
  avec `issuerRef: { kind: Issuer, name: waas-selfsigned }` pour
  **réutiliser** l'Issuer self-signed que le chart crée déjà pour le
  webhook de l'opérateur
  (`helm/waas/templates/operator-webhook.yaml:5-11`,
  `{{ .Release.Name }}-selfsigned`, release `waas` en dev → ingress et
  Issuer dans le même namespace, l'annotation `cert-manager.io/issuer`
  suffit). Attention : cet Issuer est gaté par
  `operator.webhook.enabled` — vérifie qu'il est actif en dev (il l'est
  par défaut, l'opérateur en a besoin). S'il s'avérait désactivable,
  bascule sur un petit manifest dev-only
  (`hack/dev/selfsigned-clusterissuer.yaml`, `kubectl apply` dans
  `dev-deploy`) plutôt que de coupler.
- **Traefik (bundled k3s)** : rien à installer — il expose `websecure`
  (443) d'office et terminera le TLS avec le secret `waas-public-tls`
  créé par l'ingress-shim de cert-manager. Vérifie après déploiement que
  le `Certificate` est `Ready` et que le secret existe dans le namespace
  de la release.
- **HTTP doit continuer de marcher** : `http://waas.127.0.0.1.nip.io:8080`
  reste le chemin des smoke tests (`WAAS_SMOKE_URL`, Makefile L192) et
  de la vérif e2e Playwright — ne mets **aucune** redirection
  HTTP→HTTPS. Avec l'ingress provider de Traefik, une section `tls`
  n'éteint pas le routeur HTTP ; vérifie-le après coup (un `curl` sur
  les deux ports).
- **`Makefile` cible `dev-url`** : afficher les deux URLs, en signalant
  que le clipboard seamless exige la variante https.
- Si un doc de dev (README, `docs/`) mentionne l'URL de dev, mets-le à
  jour.

### 2. Vérification en conditions réelles (fait partie du livrable)

Sous `https://waas.127.0.0.1.nip.io:8443` (Chromium, interstitiel de
certificat accepté — le `wss://` du tunnel passe dès que l'exception
d'origine est acceptée) :

- **remote→host** : copier un texte dans le bureau distant → il doit
  arriver dans le presse-papiers du host (`writeText`, L224-226, ne
  demande pas de permission en Chromium).
- **host→remote** : copier un texte dans une appli **hors navigateur**
  → revenir sur l'onglet → Ctrl+V dans le pane. C'est ici que le
  diagnostic secondaire (ci-dessous) se départage — observe avant de
  coder : log temporairement la promesse rejetée de `readText()` au
  lieu de l'avaler (L259), et log si l'event `paste` se déclenche.
- Documente le protocole et les résultats observés dans le commit ou
  dans `docs/studies/` — c'est un comportement de modèle de permission
  navigateur, non simulable en jsdom.

### 3. Seulement si host→remote reste cassé sous HTTPS

Le diagnostic hérité de l'ancienne version de ce prompt prévoit deux
problèmes qui survivent au passage en HTTPS :

- `readText()` sur l'event `focus` n'est pas dans une activation
  utilisateur transitoire : si la permission `clipboard-read` n'est pas
  déjà accordée, Chrome peut rejeter silencieusement sans jamais
  afficher le prompt. Fix : tenter aussi la lecture dans un **vrai geste
  utilisateur** — le point d'entrée naturel est le `mousedown` qui fait
  déjà `container.focus()` (L289-292). Garde `focus`/connexion en plus
  (couvre le cas permission déjà accordée).
- L'event `paste` tué par `Guacamole.Keyboard` (point 3 du diagnostic) :
  soit corrige honnêtement les commentaires qui le présentent comme
  filet universel (`DesktopPane.tsx:243-244`, `lib/clipboard.ts:12-13`),
  soit — seulement si c'est propre — laisse passer le défaut navigateur
  sur la combinaison Ctrl+V sans retirer le relais de la frappe au
  bureau distant. La correction de commentaire seule est un fix valide.

**Ne retire jamais** le relais clavier (`keyboard.onkeydown`/`onkeyup`) :
c'est lui qui permet à l'appli DANS le workspace d'exécuter son propre
paste — c'est le comportement qui marche déjà.

## Contraintes

- Ne touche pas au chemin kasmvnc (déjà livré, cf. en-tête) — mais
  profite du HTTPS pour vérifier que son clipboard marche désormais, et
  note le résultat.
- Ne régresse ni le fallback manuel de l'overlay (ultime recours
  Firefox / permission refusée), ni la gestion du focus multi-pane (le
  clic ne donne le clavier qu'à CE pane), ni les smoke tests HTTP.
- Pas de nouvelle dépendance ; pas de cert à installer dans le trust
  store du poste (l'avertissement navigateur accepté suffit).
- Le chemin prod (`ingress.tls` avec un vrai issuer) ne doit pas changer
  de comportement : tout se joue dans `hack/dev/` + Makefile.

## Tests

- `make dev-reset && make dev-bootstrap` (ou cluster edit + dev-deploy)
  passe et `dev-url` affiche les deux URLs ; `curl -k` répond en 200/302
  sur les deux ports.
- Protocole manuel du § 2 (les deux sens, Chromium) — documenté.
- Non-régression : scénario intérieur-workspace intact ; smoke
  (`make smoke`) toujours vert en HTTP ; Vitest existants
  (`DesktopPane`/`SessionOverlay`) verts.

## Points ouverts (ton arbitrage)

- Si le § 3 s'avère nécessaire : documenter les limites d'`onPaste` vs
  le rendre réellement fonctionnel — tranche selon ce que tu observes.
- `waas.localhost` en HTTP serait aussi un contexte sécurisé (zéro
  cert, zéro warning) mais a été écarté : le HTTPS de dev est plus
  proche de la prod, exerce `wss://` + terminaison TLS Traefik, et
  reste utile pour tester depuis une autre machine. Ne rebascule pas
  sur cette option sans raison forte — si tu en vois une, dis-le au
  lieu de changer silencieusement.
