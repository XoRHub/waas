---
name: verify
description: Drive the WaaS portal on the local k3d dev env to verify a change end-to-end (operator + api-server + frontend).
---

# Verify a change on the k3d dev environment

## Build & launch

- Env up already? `k3d cluster list` shows `waas-dev`; `kubectl get pods -n waas` (context `k3d-waas-dev`).
- Cold start: `make dev-bootstrap`. Inner loop after code changes: `make dev-reload`
  (rebuilds operator/api-server/frontend images, reimports into k3d, re-applies
  CRDs + chart, rollout-restarts the four deployments — CRD changes DO reach the
  cluster, dev-deploy applies `helm/waas/crds/` explicitly).
- Wait for `kubectl -n waas rollout status deploy/waas-operator deploy/waas-api-server deploy/waas-frontend`.

## URLs & credentials

- `http://waas.127.0.0.1.nip.io:8080` — smoke tests, no secure context.
- `https://waas.127.0.0.1.nip.io:8443` — self-signed, needed for clipboard flows.
- Login `admin` / `admin123` (from `hack/dev/values-dev.yaml`).

## API driving

```sh
TOKEN=$(curl -s -X POST http://waas.127.0.0.1.nip.io:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['accessToken'])")
curl -s http://waas.127.0.0.1.nip.io:8080/api/v1/catalog -H "Authorization: Bearer $TOKEN"
```

All responses are wrapped in `{"data": ...}`.

## Browser driving (Playwright, headless chromium)

Install `playwright` in a scratch dir (`npm i playwright`), browsers are usually
cached in `~/.cache/ms-playwright`. Gotchas that cost time:

- **Never wait for `networkidle`** — the SSE `/api/v1/events` stream keeps the
  network busy forever. Wait for concrete selectors instead.
- The **username input has no `type` attribute**: CSS `input[type="text"]` does
  NOT match it. Use `page.locator('input:not([type="password"])').first()`.
- **Admin lands on `/admin` after login** — click the "Back to portal" button to
  reach the user portal (where "New workspace" lives).
- Guacamole canvas is black in headless screenshots (known); regular DOM
  screenshots are fine.
- Seed data: `hack/dev/images-dev.yaml` (re-apply with `-n waas` after deleting
  test WorkspaceImages), templates seeded by the chart.

## Cleanup

Delete any CRs/ConfigMaps you created, then `kubectl apply -n waas -f hack/dev/images-dev.yaml`
to restore the seed catalog if you touched it.
