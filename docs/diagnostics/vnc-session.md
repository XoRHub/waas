# Diagnostic — session VNC inutilisable (artefacts noirs, aucun input)

**Statut : corrigé.** Concerne toute session guacd (VNC **et** RDP, SSH à venir) :
le défaut était dans le proxy WebSocket, pas dans le protocole desktop.

## Symptômes

- L'écran distant s'affiche partiellement, couvert de zones noires.
- Souris et clavier sans effet (ou clics décalés).
- Écran qui gèle après quelques secondes ; parfois déconnexion sèche.

## Cause racine

`wwt` relayait le flux guacd → navigateur par blocs TCP bruts de 32 Ko,
chacun envoyé tel quel comme message WebSocket (`proxy.pipe()`).

Or guacamole-common-js (`Guacamole.WebSocketTunnel.onmessage`) parse **chaque
message WebSocket comme une suite autonome d'instructions complètes** — c'est
un invariant du transport WebSocket de Guacamole, garanti dans la stack
officielle par le tunnel serveur qui découpe aux frontières d'instructions.
Un message coupé au milieu d'une instruction produit :

- au mieux des éléments parasites, silencieusement ignorés par le client →
  instructions de dessin (`png`/`blob`) perdues → **zones noires** ;
- la perte des `sync`/acks → guacd cesse d'envoyer des frames → **gel** ;
- au pire `close_tunnel("Incomplete instruction")` → le tunnel droppe
  silencieusement tout envoi ultérieur → **plus de souris ni clavier** alors
  que le canvas reste affiché.

Toute frame > 32 Ko (garanti dès le premier framebuffer 1920×1080) déclenchait
le problème. Les petites mises à jour passaient — d'où une session qui
« marchait presque ».

### Défauts secondaires corrigés en même temps

1. **Coordonnées souris non compensées du scale** (`DesktopPane.tsx`) : le
   display est mis à l'échelle du panneau (`display.scale(...)`) mais les
   événements souris partaient en pixels écran → clics décalés. Les
   coordonnées sont maintenant divisées par `display.getScale()`.
2. **Ping interne du tunnel forwardé à guacd** : le tunnel JS émet toutes les
   500 ms une instruction interne (opcode vide : `0.,4.ping,…;`) destinée au
   *endpoint* du tunnel, qui doit répondre par un ping identique. wwt la
   transmettait brute à guacd. Il l'intercepte désormais : écho au navigateur,
   jamais forwardée.
3. **Blacklist TigerVNC** (corrigé séparément, commit `c21d40c`) : la probe
   TCP Kubernetes comptait comme échec d'auth VNC ; au 5ᵉ, TigerVNC
   blacklistait la source et bloquait aussi guacd → connexions refusées.
   `-UseBlacklist=0` (port ClusterIP uniquement, gaté par le connection token).

## Correctif

- `wwt/internal/guac/framing.go` : `Framer` accumule le flux guacd et n'émet
  que des messages WebSocket se terminant sur une frontière d'instruction
  (scan des préfixes de longueur, comptés en points de code Unicode, sans
  parse complet). Flux corrompu ⇒ fermeture propre de la session.
- `wwt/internal/proxy/proxy.go` : `pipe()` utilise le `Framer` (guacd→ws),
  intercepte les messages internes `0.` (ws→guacd) et échoifie les pings.
- `frontend/src/components/DesktopPane.tsx` : mapping souris scale-aware.

## Reproduire / valider

1. **Repro (avant fix)** : ouvrir une session VNC 1920×1080 avec un fond
   d'écran chargé. DevTools → Network → WS : des messages entrants ne se
   terminent pas par `;` ; console : `Incomplete instruction` possible.
2. **Validation (après fix)** :
   - chaque message WS entrant se termine par `;` et commence par un préfixe
     de longueur valide ;
   - plus de zones noires après un plein redraw (bouger une fenêtre sur tout
     l'écran) ;
   - souris précise sur les quatre coins à différents scales (redimensionner
     le panneau / split view) ; clavier OK après clic dans le panneau ;
   - logs guacd (`guacd -L debug`) : réception des events `key`/`mouse`,
     aucune instruction inconnue « ping ».
3. **Régression automatisée** : `wwt/internal/guac/framing_test.go` (splits
   adverses byte-à-byte, runes multi-octets) et
   `wwt/internal/proxy/proxy_test.go::TestFramesEndOnInstructionBoundaries`
   (guacd factice qui « goutte » une grosse instruction + écho ping + le ping
   n'atteint jamais guacd).
4. **RDP** : même chemin de code, même correctif ; dérouler la même grille
   (artefacts, input, précision souris) sur une session RDP pour confirmer.
