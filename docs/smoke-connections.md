# Test de connexion par protocole (gate de livraison)

`test/smoke` établit une **vraie session** guacd pour chaque protocole
(vnc, rdp, ssh) à travers la stack complète — API publique, opérateur,
placement en namespace dédié, wwt, guacd, image de bureau. Il existe parce
que « le workspace est Ready » ne prouve rien sur le chemin de session :
une NetworkPolicy qui rejette guacd, une `Status.Address` fausse ou des
credentials cassés passent la readiness et ne meurent qu'à la connexion —
exactement la régression « connection closed » de juillet 2026 (voir
`docs/diagnostics/placed-namespace-netpol.md`).

## Ce que fait le test, par protocole

1. login sur l'API publique (compte de validation) ;
2. choix du template : premier template du catalogue servant le protocole
   (préférence au template dont c'est le protocole par défaut) ;
3. création d'un workspace, attente de la phase `Running` — si le
   template a un cron de downtime et que le workspace naît `Stopped`,
   le test fait ce que fait le portail : un `resume`, puis attend ;
4. `POST /connect {protocol}` puis ouverture du WebSocket wwt avec le
   token de connexion ;
5. lecture du flux guacd : **succès à la première instruction `sync`**
   (le client protocolaire de guacd a réellement atteint le bureau et
   poussé une frame) ; échec sur instruction `error`/`disconnect`, ou
   fermeture du flux. Une socket ouverte ne suffit pas : guacd n'ouvre la
   connexion vers le bureau qu'après son handshake avec wwt ;
6. suppression du workspace (toujours, même en échec).

## Lancer

```sh
# contre l'environnement k3d de dev (URL et credentials par défaut) :
make smoke

# contre un autre environnement :
WAAS_SMOKE_URL=https://waas.example.com \
WAAS_SMOKE_USER=validation WAAS_SMOKE_PASSWORD=... \
go test -count=1 -v ./test/smoke/
```

Variables : `WAAS_SMOKE_URL` (sans elle le test se **skip** — `go test
./...` reste utilisable hors ligne), `WAAS_SMOKE_USER`/`WAAS_SMOKE_PASSWORD`
(défaut admin/admin123 du dev), `WAAS_SMOKE_PROTOCOLS` (défaut
`vnc,rdp,ssh`), `WAAS_SMOKE_READY_TIMEOUT` (défaut 5m),
`WAAS_SMOKE_PLATFORM_NAMESPACE` (défaut `waas` — voir ci-dessous).

## Sous-test `vnc-audio` : le port PulseAudio (4713)

Une session VNC vivante ne prouve rien sur le port audio : guacd le
compose séparément, et un Service auquel il manque le port échoue en
silence (session OK, pas de son). Quand un template expose le port audio
(`protocols[].exposeAudioPort`, seedé en dev par `ubuntu-firefox` — un
navigateur est aussi le test manuel naturel : lancer une vidéo), le
sous-test `vnc-audio` établit la session puis vérifie que
`<service>:4713` répond en TCP **depuis le namespace plateforme** — le
chemin exact de guacd, NetworkPolicy default-deny comprise. La sonde est
un pod éphémère `kubectl run` (busybox `nc -z`) : contrairement au reste
du smoke, ce sous-test a besoin de `kubectl` dans le PATH (skip sinon,
comme quand aucun template n'expose le port).

## Critère de livraison

Une itération n'est pas livrable si `make smoke` ne passe pas sur
l'environnement de validation. Intégration CI (GitLab) : un job de stage
`validate` qui monte le k3d éphémère (`make dev-bootstrap`) puis lance
`make smoke`. C'est le montage le
plus léger qui reste fiable : il utilise exactement le chemin du
navigateur (même ingress, même WebSocket), sans navigateur ni Selenium.

Le catalogue de l'environnement de validation doit couvrir chaque
protocole (le test échoue si un protocole n'a pas de template — c'est
voulu : un catalogue qui perd un protocole est une régression).
