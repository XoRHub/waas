# Prompt Fable 5 — Feature 6 : exposer le port audio (PulseAudio) quand server-audio est activé

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

Le commit `61c8b29d184a feat(images): VNC audio via une PulseAudio non privilégiée` a ajouté le support audio serveur pour les sessions VNC. Le paramètre guacd `enable-audio` (`operator/pkg/params/params.go:116`, bool, VNC, `TierUI`) et `audio-servername` (ligne 121, string, VNC, `TierAdvanced`) existent déjà dans le registre — mais **rien n'ouvre le port réseau que PulseAudio écoute**, ni côté Pod (`ContainerPort`) ni côté `Service` Kubernetes, ni dans l'UI, ni dans le CR. Le paramètre guacd peut donc être activé sans que le port applicatif associé soit jamais joignable.

## Ce qui existe déjà (à connaître avant de coder)

**Le port PulseAudio est fixe, câblé dans l'image, jamais exposé côté cluster** :
- `waas-images/base/ubuntu/rootfs/etc/waas/pulse/default.pa.tpl:16` : `load-module module-native-protocol-tcp port=4713 auth-anonymous=1` — port **4713**, en dur, pas configurable par variable d'environnement.
- `waas-images/base/ubuntu/Dockerfile:23` (`EXPOSE ... 4713`) et `:120` (`WAAS_AUDIO_ENABLED=1`) : toute la stack PulseAudio est on/off via `WAAS_AUDIO_ENABLED`, mais `EXPOSE` dans un Dockerfile ne fait que documenter — ça n'ouvre rien côté Kubernetes.
- `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-entrypoint:98-121` : démarre le programme supervisord PulseAudio seulement si l'audio est activé.

**Le CRD n'a qu'un seul port par protocole, pas de liste** : `WorkspaceProtocol` (`operator/api/v1alpha1/workspacetemplate_types.go:198-242`) a exactement un champ `Port int32` (lignes 210-213). Aucun champ `extraPorts`/`additionalPorts` n'existe nulle part dans `workspace_types.go` ni `workspacetemplate_types.go`.

**L'opérateur construit les ports 1:1 depuis cette unique valeur** :
- Conteneur : `operator/internal/controller/workload.go:69-73` — `ports = append(ports, corev1.ContainerPort{Name: p.Name, ContainerPort: p.Port})` pour chaque protocole de `EffectiveProtocols()`. Le port 4713 n'est jamais ajouté.
- Service : `operator/internal/controller/workspace_controller.go:813-821` (`ensureService`) — même logique, un `ServicePort` par protocole déclaré, même source.

**Point d'attention critique, à connaître avant de toucher au Service** : `ensureService` **ne crée le `Service` qu'une seule fois** — `if err == nil { return nil }` (`workspace_controller.go:806-808`) : si le Service existe déjà, il n'est **jamais mis à jour**, même si la liste de ports change ensuite. C'est cohérent avec un écart déjà documenté par l'audit sur le `podTemplate` (`create-only`, `docs/studies/audit-2026-07.md` §Operator) — mais pour cette feature, ça veut dire qu'ajouter le port audio dans le code ne suffira pas à le faire apparaître sur un workspace **déjà existant** tant que le Service create-only n'est pas aussi corrigé (au moins pour les ports), sinon l'admin devra recréer le workspace pour que le port apparaisse.

**Environnement de dev** : `hack/dev/templates-dev.yaml` ne référence l'audio nulle part ; tous les templates n'ont qu'un port par protocole (`vnc:5901`, `rdp:3389`, `ssh:2222`, `kasmvnc:6901`).

**Smoke tests** : `test/smoke/smoke_test.go` (`TestProtocolConnections`, ~ligne 47) pilote `WAAS_SMOKE_PROTOCOLS` (défaut `vnc,rdp,ssh,kasmvnc`), un sous-test `t.Run(protocol, ...)` par protocole via `connectOnce` (~ligne 78-128) : create → poll Running → `POST .../connect` → établissement WS guacd. C'est le squelette à étendre, pas un nouveau pattern à inventer.

## Ce qu'il faut livrer

### A. Exposer le port dans le CRD, l'opérateur, le Service

Ajoute la notion de port auxiliaire côté `WorkspaceProtocol` (`workspacetemplate_types.go`) — recommandation : un champ simple lié à l'audio plutôt qu'un mécanisme générique `extraPorts[]` (le port PulseAudio est fixe à 4713, pas configurable ; sur-généraliser maintenant pour un seul cas d'usage n'est pas justifié — voir "Points ouverts" si tu identifies un vrai besoin de généricité). Câble ce champ dans :
- `workload.go:69-73` : ajoute le `ContainerPort` 4713 quand le port audio est demandé pour le protocole VNC.
- `ensureService` (`workspace_controller.go:813-821`) : ajoute le `ServicePort` correspondant, **et corrige le create-only au moins pour la convergence des ports** (un `Service` existant doit voir ses `Spec.Ports` mis à jour si la liste attendue change) — sinon cette feature ne fonctionnera jamais sur un workspace déjà provisionné avant l'activation de l'audio.

### B. UI : le menu d'ajout du port apparaît quand `enable-audio` est activé

`ParamField.tsx`/`ProtocolTabs.tsx` rendent aujourd'hui les params de façon générique, sans logique conditionnelle inter-champs (aucune UI n'affiche/masque un champ selon la valeur d'un autre). Ajoute ce comportement conditionnel : quand `enable-audio` est réglé sur `true` dans le formulaire (`CreateWorkspaceDialog`, `TemplatesPage`, `ConnectionSettingsDialog` — tous consomment `ProtocolParamsForm`), fait apparaître une section/case à cocher explicite "Exposer le port audio (4713)" qui pilote le nouveau champ du CRD. Documente dans `ParamField.tsx`/`ProtocolTabs.tsx` (commentaire) ce premier cas de rendu conditionnel, en gardant à l'esprit que d'autres features à venir pourraient vouloir grouper/lier des champs entre eux (voir Feature 7 de cette série) — ne construis pas un mécanisme générique de dépendances entre champs si un simple `enable-audio && <Champ port />` suffit ici.

### C. Environnement de DEV : un CR dédié + smoke

- Ajoute dans `hack/dev/templates-dev.yaml` un template (ou une variante d'un template VNC existant) avec `enable-audio: true` et le port exposé, pour pouvoir tester manuellement en dev.
- Étends `test/smoke/` : un sous-test qui, pour un template audio-enabled, vérifie que le port 4713 est effectivement joignable après connexion (dial TCP direct, ou via un client PulseAudio minimal type `pactl`) — même structure que les sous-tests protocole existants dans `connectOnce`.

## Contraintes à respecter

- N'élargis pas l'exposition du port au-delà du cluster : comme les autres ports de session, il ne doit être accessible qu'en interne (Service ClusterIP), jamais via l'Ingress public (`helm/waas/templates/ingress.yaml`/`httproute.yaml` n'allow-listent que des chemins explicites — ne les touche pas).
- Le fix du Service create-only doit rester scopé à la convergence des ports pour cette feature — ne te lance pas dans un refactor général de convergence du `podTemplate` (écart déjà documenté séparément par l'audit, hors périmètre ici).
- Tests Go sur le nouveau champ CRD (validation webhook si applicable), sur `workload.go`/`ensureService`. Test vitest sur le nouveau rendu conditionnel du champ port dans `ParamField.tsx`/`ProtocolTabs.tsx`.
- i18n pour toute nouvelle chaîne UI.

## Points ouverts (ton arbitrage)

- Champ CRD dédié (`AudioPort`/booléen "exposer le port audio") vs mécanisme générique `extraPorts []PortSpec` réutilisable pour de futurs besoins similaires — recommandation ci-dessus va vers le champ dédié (YAGNI), à réviser seulement si tu vois un second cas d'usage concret dans le repo qui justifierait la généralisation.
- Portée du fix "Service create-only" : le corriger uniquement pour les ports (minimal, scope de cette feature) vs pour l'ensemble de `Spec` (le vrai chantier déjà identifié par l'audit, plus risqué et hors périmètre) — reste sur le minimal sauf si tu juges qu'un correctif partiel introduirait une incohérence pire que le statu quo.
