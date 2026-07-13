# WaaS Helm chart

Single-chart install: operator, API server, WebSocket proxy (wwt), frontend,
guacd and PostgreSQL. The only external prerequisite is cert-manager
(KubeVirt is auto-detected, never required).

## Install

From the OCI registry (published on every chart release, independent
SemVer from the app — see `Chart.yaml`'s `version` vs `appVersion`):

```sh
helm install waas oci://ghcr.io/xorhub/waas/charts/waas --version <chart-version>
```

From source:

```sh
helm install waas ./helm/waas
```

## Upgrade / uninstall

```sh
helm upgrade waas oci://ghcr.io/xorhub/waas/charts/waas --version <chart-version>
helm uninstall waas
```

## Configuration

See `values.yaml` for all options (image registry/tag, workspaces namespace
and placement pattern, admin policy bootstrap, default catalogs, ingress,
cert-manager issuer, etc.) — every value is documented inline.

## Production note

ArgoCD deployments track a `vX.Y.Z` git tag directly (not this OCI
artifact) — see [docs/ci-github.md](../../docs/ci-github.md) for the
release/promotion pipeline. This chart is meant for external
`helm install`/`helm pull` consumers.
