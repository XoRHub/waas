# Frontend — « un composant, des capacités »

Modèle cible issu de l'itération de convergence : les workspaces
in-cluster et les remote workspaces partagent **les mêmes composants**,
paramétrés par un descripteur commun. Le frontend ne branche jamais sur
le type ; ce qui diffère légitimement est déclaré **une fois** comme
capacité.

## Le descripteur : `SessionTarget` (`lib/target.ts`)

```ts
SessionTarget = {
  id, kind, displayName, subtitle, connectUrl,
  protocols: [{name, port, default, params, userParams}],
  defaultProtocol,
  capabilities: { pause, wake, splitView, connectionSettings,
                  editEndpoint, hasPhase },
}
```

Deux adaptateurs — `targetFromWorkspace` / `targetFromRemote` — sont les
SEULS endroits qui connaissent la forme des deux modèles API. Côté API,
les deux kinds exposent la même shape `protocols[]` (les remote sont
multi-endpoints depuis la migration `add_remote_protocols` ; les lignes
legacy synthétisent leur endpoint unique à la lecture).

## Les composants uniques et leurs contextes

| Composant | Contextes | Différences portées par |
|---|---|---|
| `SessionCard` + `FolderedGrid` | cards in-cluster **et** remote | capacités (badge si `hasPhase`, WoL si `wake`) + slots d'actions |
| `useProtocolSwitch` | chips de card + overlay, les deux kinds | préférence `workspaceSettings[id].protocol`, confirmée en session |
| `ProtocolTabs` + `ProtocolParamsForm` | création, connection settings, dialog remote, éditeur de template admin | `allowList` (userParams vs admin/owner), `placeholders` (valeurs verrouillées), `renderParamExtra` (checkbox admin) |
| `SessionOverlay` | session in-cluster **et** remote | capacités (split view), stockage des params (prefs vs endpoint serveur) |
| `Dialog`, `useEscape` | tous les dialogs/menus | — |
| `lib/lifecycle` | badge/boutons/polling | dérive Pausing…/Resuming… de l'écart spec/status |

**Règle pour les prochaines évolutions** : une fonctionnalité de card,
d'overlay ou de formulaire de protocole s'écrit UNE fois contre
`SessionTarget`/`ProtocolParamsForm`. Un besoin spécifique à un type =
un nouveau flag dans `TargetCapabilities` + un rendu conditionnel dans le
composant unique — jamais un composant parallèle. Ce qui reste
volontairement séparé : WoL, credentials machine, édition d'endpoint
(remote) ; pause/resume, settings de connexion, split view (in-cluster).

Les contrôles restent **côté serveur** : le webhook/policy filtre les
protocoles et paramètres, `Connect` valide le protocole choisi contre ce
que la cible déclare — l'unification frontend n'a déplacé aucun
enforcement vers le client.

## Rafraîchissement d'état

- **Source de vérité** : le `status` du CR (projeté par l'api-server, qui
  force `Terminating` pendant un teardown). L'UI n'invente pas d'état :
  elle étiquette l'écart intent/réalité (`Pausing…`, `Resuming…`) et
  converge.
- **SSE** `GET /api/v1/events` : un watch Kubernetes partagé côté
  api-server relaie chaque changement de Workspace ; les mutations remote
  (DB, écrivain unique) notifient directement. Les messages ne portent
  que des *kinds* — le client ré-interroge l'API autorisée, rien ne fuit.
  Auth : même access token en query (`EventSource` ne pose pas de
  headers), même vérification middleware. Heartbeat 25 s,
  `X-Accel-Buffering: no` pour nginx (traefik streame nativement).
- **Polling conservé en fallback** : 3 s pendant la convergence, 15 s
  sinon (workspaces), 30 s (remote).

## Matrice de validation protocole (livrable 6)

| Surface | In-cluster | Remote |
|---|---|---|
| Création | tabs + params/protocole + radio « se connecter avec » (verrouillée si non overridable) | tabs + port/endpoint + défaut + « add protocol » |
| Card | chips de switch (si >1 protocole servi) | chips de switch (si >1 endpoint) |
| Connection settings | tabs + params (allow-list userParams, admin bypass) | via le dialog d'édition (mêmes tabs) |
| Session (overlay) | switch confirmé + params reconnect (prefs) | switch confirmé + params reconnect (endpoint serveur) |
| Admin template | tabs + params + checkbox user-overridable | n/a (pas de template) |

Couverture automatique : `lib/target.test.ts` (adaptateurs, synthèse
legacy, capacités), `lib/lifecycle.test.ts` (états dérivés),
`remote_workspace_service_test.go` (multi-protocoles : round-trip,
connect sur endpoint non-défaut, résolution port/params, refus des
non-déclarés, compat entrée legacy), `event_hub_test.go` (fan-out par
owner/admin, relais du watch). Passe manuelle : dérouler la matrice
ci-dessus sur k3d (`make dev-reload`), plus pause/resume/suppression et
une transition cron pour vérifier la convergence sans recharger la page.
