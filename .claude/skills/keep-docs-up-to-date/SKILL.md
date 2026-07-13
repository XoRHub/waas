---
name: keep-docs-up-to-date
description: Check whether the change in progress makes a doc stale, and patch that doc in the same session. TRIGGER when the change touches Makefile targets, .mise.toml, quickstart/dev-env commands, external dependency URLs, .github/workflows/ci.yml, a hard architecture boundary (new component, new K8s-API/DB access path), commit/contribution conventions, or behavior already described in an existing docs/*.md (clipboard, governance, placement, testing, image catalog, ...). SKIP for pure code changes with no user-visible or workflow-visible effect — refactors, test-only changes, dependency bumps, formatting, and changes to generated docs (operator/docs/guacd-parameters.md regenerates via make docs-params).
---

# Keep docs in sync with the change in progress

A change that makes documentation stale must fix that documentation in
the **same session/commit series** — not a follow-up someone forgets.
The history shows the pattern: `docs: document X in Y.md` commits paired
with the code they describe.

## Change → doc map

| Change in progress | Doc(s) to check/patch |
|---|---|
| Makefile targets, `.mise.toml`, quickstart or dev-env commands, external dependency URL (e.g. waas-images) | `README.md` (Quickstart) + `CONTRIBUTING.md` (pre-PR commands) |
| `.github/workflows/ci.yml` (jobs, gates, release flow) | `docs/ci-github.md` + the one-paragraph CI summary in `README.md` |
| New component, or a change to a hard boundary (operator↔DB, api-server↔pods, frontend↔K8s API, guacd exposure) | `README.md` (Architecture + hard boundaries) + `AGENTS.md` |
| Test strategy: new tier, new harness, envtest/smoke changes | `docs/testing.md` |
| Commit convention or contribution process | `CONTRIBUTING.md` + `AGENTS.md` (keep the two consistent) |
| Behavior covered by a topical `docs/*.md` (clipboard, governance, placement, volumes, workspace lifecycle/deletion, image catalog, kasmvnc, observability, session-resize, smoke-connections, remote-workspaces, frontend-capabilities) | that specific file only |

## How to patch

- **Smallest accurate diff**: update the sentences the change falsified,
  nothing else. Never rewrite a whole doc because you were nearby.
- Don't copy values that live in a pinned source (`.mise.toml` versions,
  CI job details) into prose — link to the source instead.
- `operator/docs/guacd-parameters.md` is generated: run
  `make docs-params`, don't edit it.
- If several docs cover the topic, patch the owning doc and make sure
  the others still just link to it (no duplication between `README.md`,
  `CONTRIBUTING.md`, `AGENTS.md` and `docs/*.md`).
- Commit the doc update with the code change it belongs to, or as an
  adjacent `docs:` commit in the same series.
