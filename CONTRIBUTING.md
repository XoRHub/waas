# Contributing to WaaS

Thanks for contributing! This file covers the workflow; the technical
references live next to the code and are linked from here rather than
duplicated.

## Dev environment

Follow the [Quickstart in the README](README.md#quickstart-local-dev).
Toolchain versions are pinned in [`.mise.toml`](.mise.toml) (`mise install`
sets everything up) — CI installs from the same file (`jdx/mise-action`),
so local and CI can't drift.

## Before opening a PR

Run locally what CI will run (details: [docs/ci-github.md](docs/ci-github.md)):

```sh
make build test                  # all Go modules (unit tier)
(cd <module> && golangci-lint run)   # per touched Go module: shared, operator, api-server, wwt
                                     # (includes the gofmt formatter check)
make generate manifests docs-params generate-types
git diff --exit-code             # generated code must be committed (CRDs, RBAC, types.gen.ts)
helm lint helm/waas              # if the chart changed
```

Frontend, if touched:

```sh
cd frontend
npm run lint && npm run format:check && npm run typecheck && npm test
```

The testing strategy (which tier a new test belongs to, and how to run
envtest and the smoke suite) is described in
[docs/testing.md](docs/testing.md).

## Commits

[Conventional Commits](https://www.conventionalcommits.org/), enforced
by release-please for versioning: `<type>(<scope>): <description>`, or
`<type>: <description>` when no scope fits. Types in use: `feat`, `fix`,
`docs`, `refactor`, `chore`, `ci`, `style`, `test`. Scopes in use:
`operator`, `api-server`, `frontend`, `helm`, `shared`. Breaking
changes take a `!`.

Real examples from the history:

```
feat(frontend): visual template picker with vendored icons
fix(operator): avoid gofmt doc-comment quote mangling in a CEL rule
ci: split release-please into app and chart packages
refactor!: split waas-images/ out into its own repository
```

Keep commits **atomic**: one logical change per commit, never a commit
that doesn't build or pass tests on its own.

### Developer Certificate of Origin

By contributing you certify the [DCO](https://developercertificate.org/):
you wrote the change or have the right to submit it under Apache-2.0.
Sign off every commit with `git commit -s`, which appends:

```
Signed-off-by: Your Name <you@example.com>
```

## Pull requests

- Branch from `main`.
- CI must be green — the pipeline and its blocking gates (lint, tests,
  generated-code drift, security scans) are documented in
  [docs/ci-github.md](docs/ci-github.md).
- If your change affects the README, this file, `AGENTS.md` or a
  `docs/*.md`, update the doc in the same PR.
