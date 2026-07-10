# Prompt Fable 5 — Feature 2 : métriques Prometheus + dashboards Grafana

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

WaaS a trois composants Go qui tournent en pods dans le cluster : `wwt/` (proxy websocket, port unique `:8081`), `api-server/` (backend REST chi, port unique `:8080`), `operator/` (controller-runtime, déjà scaffoldé avec un endpoint metrics controller-runtime standard sur `:8080` — voir plus bas). **Aucun des trois n'expose de métriques applicatives aujourd'hui** — vérifié : zéro import `prometheus` dans tout le module Go du repo, zéro `ServiceMonitor`/`PodMonitor` dans `helm/waas/`, zéro référence Grafana en dehors du brief historique. C'est un chantier entièrement neuf.

## Ce qui existe déjà (à connaître avant de coder)

**operator (`operator/cmd/main.go`)** : le manager controller-runtime est déjà configuré avec `metricsserver.Options{BindAddress: ":8080"}` (`ctrl.Options` ligne ~59) — c'est le port `metrics` déjà déclaré dans `helm/waas/templates/operator.yaml:176-177` sur le Deployment. Ça expose déjà, sans rien faire, les métriques standard controller-runtime/client-go (`controller_runtime_reconcile_total`, `workqueue_*`, etc.) — **mais il n'y a aucun `Service` Kubernetes exposant ce port** (seul un `Service` webhook existe, `helm/waas/templates/operator-webhook.yaml:29-42`, port 443→`webhook`). Sans Service, un `ServiceMonitor` ne peut rien cibler ; un `PodMonitor` (sélection par labels de Pod, pas besoin de Service) fonctionne directement — c'est probablement le bon outil pour l'opérateur.

**api-server (`api-server/cmd/api-server/main.go`)** : un seul `http.Server` avec `ReadHeaderTimeout` seulement (écart déjà noté dans `docs/studies/audit-2026-07.md` §api-server — ajoute `ReadTimeout`/`IdleTimeout` si tu y touches, mais ce n'est pas l'objet de cette feature). Le routeur chi (`api-server/internal/server/router.go`) monte `/healthz`, `/readyz`, `/.well-known/jwks.json` hors `/api/v1`, puis tout le reste sous `/api/v1` derrière `middleware.Auth`. Le Service helm (`helm/waas/templates/api-server.yaml:242-255`) expose le port `http` (8080) — un `ServiceMonitor` peut cibler ce Service directement.

**wwt (`wwt/cmd/main.go`)** : `http.ServeMux` brut, `/ws` + `/kasm/` + `/healthz` + `/readyz` sur `:8081`. Service helm (`helm/waas/templates/wwt.yaml:58-71`) expose le port `http` (8081) — même remarque, `ServiceMonitor` OK.

**Exposition publique — vérifié, ne rien casser :** `helm/waas/templates/ingress.yaml` et `httproute.yaml` n'allow-listent que des chemins explicites (`/api`, `/.well-known/jwks.json`, `/ws`, `/kasm`, `/` pour le frontend). Un nouvel endpoint `/metrics` monté sur le même port que le trafic applicatif **n'est déjà pas atteignable depuis l'extérieur** tant que tu ne l'ajoutes pas à ces fichiers — ne l'y ajoute pas. Le scraping Prometheus se fait exclusivement en cluster (via le Service ClusterIP ou directement les IP de Pod pour le PodMonitor de l'opérateur), jamais via l'Ingress public.

## Ce qu'il faut livrer

### A. Métriques applicatives dans les 3 composants

Ajoute `github.com/prometheus/client_golang` (déjà présent transitivement dans `go.sum` via des dépendances de `operator`/`api-server`, à promouvoir en dépendance directe) et un endpoint `/metrics` (`promhttp.Handler()`) sur le port existant de chaque composant — pas de port séparé à créer, réutilise `:8080`/`:8081`/`:8080` (operator).

Pas de métriques "gratuites" au-delà de ce que controller-runtime donne déjà à l'opérateur : chaque composant doit exposer des métriques **métier**, ancrées dans du code réel existant plutôt qu'inventées. Pistes concrètes (ajuste les noms/labels à ton goût, garde le principe — une métrique par signal qui a déjà une trace dans le code) :

- **operator** : gauge du nombre de workspaces par phase (le `WorkspaceReconciler` connaît déjà `status.Phase` à chaque reconcile — voir `operator/internal/controller/workspace_controller.go`), compteur de workspaces en dérive (`TemplateDrifted`, cf. `docs/adr/0001-template-boundary-convergence.md` et le prompt Feature 1 si tu l'implémentes aussi), compteur de réclamations du `NamespaceJanitor` (`operator/internal/controller/namespace_janitor.go`), compteur d'échecs de teardown (l'Event `TeardownFailed` existe déjà, `docs/workspace-deletion.md` — miroir en métrique), durée/erreurs par type de reconciler si tu veux aller plus loin que les métriques controller-runtime déjà gratuites.
- **api-server** : histogramme durée+compteur requêtes HTTP par route/méthode/statut (middleware chi à ajouter dans `router.go`, avant ou après `chimiddleware.Recoverer`), gauge sessions actives (le `SessionSweeper` — mémoire projet : "goroutine WAAS_SESSION_SWEEP_INTERVAL" — connaît déjà l'état des sessions), gauge clients SSE connectés (`EventHub`, `api-server/internal/service/event_hub.go`), compteur de refus de politique (chaque refus passe déjà par `s.audit.Record(ctx, actor, "workspace.denied", ...)` dans `workspace_service.go` — ajoute un compteur au même point d'appel plutôt que de dupliquer la logique de détection).
- **wwt** : gauge de tunnels actifs (guacd + kasm, les deux handlers existent dans `wwt/internal/proxy` et `wwt/internal/kasm`), compteur d'octets proxyés, compteur d'échecs de validation de token de connexion (`proxy.ValidateConnectionToken`, déjà exporté et partagé entre les deux chemins), compteur de blocages du `ClipboardFilter` (`wwt/internal/guac`, déjà policy-aware).

Chaque compteur/gauge ajouté doit avoir un test (le repo a une culture de tests systématiques — `wwt` est à 87/86,8/62,2% de couverture sur `guac`/`kasm`/`proxy`, ne fais pas baisser ça).

### B. Helm chart : PodMonitor / ServiceMonitor / annotations de scrape

Rends les trois mécanismes disponibles en toggle `values.yaml`, désactivés par défaut (cohérent avec le reste du chart — ex. `operator.webhook.enabled`, `apiServer.oidc.*` — tout est opt-in avec un commentaire explicatif) :

```yaml
metrics:
  enabled: false          # expose /metrics sur chaque composant (toujours cluster-interne, jamais via l'Ingress)
  scrapeAnnotations: false  # pose prometheus.io/scrape=true, /port, /path sur les Pods (Prometheus classique sans CRDs)
  podMonitor:
    enabled: false         # operator (pas de Service metrics) — étends aux 3 si tu préfères l'uniformité
    labels: {}             # labels additionnels pour le sélecteur du Prometheus/PrometheusOperator ciblé
    interval: 30s
  serviceMonitor:
    enabled: false         # api-server + wwt (ont déjà un Service ClusterIP)
    labels: {}
    interval: 30s
```

`metrics.enabled: false` doit couper l'endpoint lui-même (ne serve rien), pas juste cacher le manifeste K8s — évite d'exposer un endpoint non demandé par défaut. Ajoute `metrics` comme port nommé sur les Deployments concernés uniquement quand pertinent pour le `PodMonitor`/`ServiceMonitor` cible.

CRDs `PodMonitor`/`ServiceMonitor` viennent de prometheus-operator et ne sont pas forcément installées sur tous les clusters cibles — c'est pourquoi elles doivent rester opt-in et ne jamais faire échouer `helm template`/`helm install` quand désactivées (pas de `lookup` bloquant, pas de dépendance dure aux CRDs). Vérifie `helm lint` et `helm template` avec les toggles activés ET désactivés (le repo a déjà un job CI `.github/workflows/ci.yml` qui lint/template le chart — reste cohérent avec ce que couvre `docs/ci-github.md`).

### C. Dashboards Grafana

Fournis au moins un dashboard JSON par composant (ou un dashboard unique multi-composant si tu préfères — panels sur les métriques ajoutées en A) et deux modes de déploiement, en toggle `values.yaml` :

```yaml
grafana:
  dashboards:
    enabled: false
    mode: configmap        # "configmap" | "operator"
    # mode configmap: sidecar grafana (convention grafana/grafana Helm
    # chart officiel) — label grafana_dashboard: "1" sur la ConfigMap,
    # récupéré automatiquement par le sidecar de découverte.
    # mode operator: grafana-operator (grafana.integreatly.org/v1beta1
    # GrafanaDashboard) — nécessite de cibler une instance Grafana existante.
    instanceSelector:       # utilisé seulement en mode "operator"
      matchLabels: {}
    folder: WaaS
```

- Mode `configmap` : une `ConfigMap` par dashboard avec le label `grafana_dashboard: "1"` (convention du sidecar `kiwigrid/k8s-sidecar` embarqué par le chart Grafana officiel — la plupart des installs Prometheus/Grafana stack l'utilisent déjà, zéro dépendance à un opérateur).
- Mode `operator` : une CR `GrafanaDashboard` (`grafana.integreatly.org/v1beta1`) par dashboard, `spec.instanceSelector` piloté par `values.grafana.dashboards.instanceSelector`, `spec.json` = le contenu du dashboard. N'installe pas les CRDs grafana-operator toi-même (comme pour prometheus-operator, opt-in, jamais de dépendance dure).
- Les deux modes doivent rendre le **même contenu de dashboard** (pas deux JSON divergents à maintenir — factorise le JSON dans un seul fichier/`include` Helm consommé par les deux templates).

## Contraintes à respecter

- Suis le principe déjà appliqué partout dans ce chart : chaque nouvelle capacité est **opt-in par défaut**, documentée par un commentaire dans `values.yaml`, jamais un comportement qui change silencieusement l'existant.
- `gofmt` propre sur les 3 modules Go ; pas de nouveau `console.log`/`TODO`.
- Ajoute la nouvelle doc (`docs/observability.md` ou équivalent — vérifie qu'un tel fichier n'existe pas déjà avant d'en créer un) décrivant : les métriques exposées par composant, comment activer PodMonitor/ServiceMonitor/annotations, comment activer les dashboards dans les deux modes.
- Mets à jour `docs/studies/audit-2026-07.md` seulement si tu y touches directement (ce n'est pas un document vivant à maintenir à chaque feature — laisse-le tel quel sauf si une de ses lignes devient fausse).
- Le CI GitHub Actions (`docs/ci-github.md`) build par composant via un graphe de `go.work replace` — si tu ajoutes `prometheus/client_golang` en dépendance directe à `operator`/`api-server`/`wwt`, vérifie que `go mod tidy` reste cohérent dans chaque module et que le `go.work.sum` racine suit.

## Points ouverts (ton arbitrage)

- Uniformiser sur `PodMonitor` partout (plus simple, pas de dépendance à l'existence d'un Service) vs. `ServiceMonitor` pour api-server/wwt qui ont déjà un Service : les deux sont défendables, choisis et documente le choix dans `docs/observability.md`.
- Authentification de l'endpoint `/metrics` (aucune aujourd'hui côté api-server/wwt, contrairement au reste de l'API) : comme il n'est jamais exposé publiquement (voir plus haut), rester non-authentifié en cluster est cohérent avec la pratique Prometheus standard — à confirmer/documenter plutôt qu'à requestionner par défaut.
