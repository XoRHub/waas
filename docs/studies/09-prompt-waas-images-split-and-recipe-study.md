# Fable 5 Prompt — waas-images: split into a dedicated repo + simplification study (image recipe, new OS/apps, hardened/dev variant)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context. This prompt has two
different natures — **treat them as such**:

- **Part A (split)**: a mechanical, well-specified action, to be
  delivered entirely in this session.
- **Part B (study)**: a **documentation-only** deliverable.
  Don't implement anything it proposes — no new Dockerfile,
  no new CI, no hardened variant. The repo already has a
  precedent for this kind of breakdown (`docs/studies/prompt-etude1-protocol-feature-matrix.md`
  produced a pure study; each arbitrated item then became its own
  prompt numbered 01 to 08) — follow the same pattern. Once the
  study is delivered and its open points arbitrated by the user, a
  **separate follow-up prompt** will drive the implementation of the
  chosen option — it is not up to you to trigger it here.

## Repo context

`waas-images/` (root of the monorepo) is **not a blank slate**:
it's already a functional, layered OCI image build system, delivered
and iterated over several commits (`d7c2b0996802`
initial, then `b98a6dd9d`, `d49ebfd07`, `61c8b29d1`, `6326b0c24`
up to today). Before proposing anything in part B,
read in full:

- `waas-images/README.md` — contract with the Workspace CR, build
  matrix, "Adding an app image" procedure (§ 4 steps, lines
  113-141).
- `waas-images/images.yaml` — global config (OS matrix, default
  archs, trivy gate).
- `waas-images/HARDENING.md` — a checklist verifiable point by point
  (build-time / runtime / platform side) + threat model + documented
  accepted gaps.
- `waas-images/ci/generate_pipeline.py` — discovers the `manifest.yaml`
  files under `base/`, `desktop/`, `apps/`, topologically sorts them on `from`,
  generates a GitLab child pipeline (one native build job per image ×
  arch + one manifest-list merge job).
- An existing `manifest.yaml` (`waas-images/apps/firefox/manifest.yaml`
  for instance) to see the current recipe format.

## What already exists (to know before proposing anything)

- **The "recipe" format already exists**, across two files: a
  `manifest.yaml` per image (name, version, `from:` the parent, variants,
  smoke expectations) + a global `images.yaml` (OS matrix, archs,
  trivy severity). Adding an image = a new folder + Dockerfile
  + manifest, no modification of the generator. This is **not** a
  gap to be filled with an entirely new system — the study must
  judge whether this format is already sufficient for the requested
  "cooking recipe" bar, or precisely identify what's missing (a Dockerfile
  is still required for every node in the `apps/` layer — see § B.1).
- **supervisord is already the runtime orchestrator** (`tini + supervisord`,
  README.md:24); **cloud-init appears nowhere** in the repo —
  it's a VM/cloud instance provisioning tool, not a container image
  tool; don't propose it reflexively just because it's
  mentioned in the request, check whether it has a real role at that
  build-time.
- **The matrix CI already exists, but GitLab only.**
  `docs/ci-github.md:90` explicitly documents this gap: "waas-images/:
  generated GitLab child pipeline, no GitHub equivalent." —
  `docs/ci-github.md:36` likewise. This isn't an assumption, it's already
  written in black and white as a known gap.
- **An already-documented catalog gap, not to invent**:
  `gitops/governance/images.yaml:53-70` references an image
  `ubuntu-devtools` ("Dev Tools (VS Code, toolchains)", restricted to
  the `nymphe:dev` IdP group via `allowedGroups`) — but **no
  `waas-images/apps/devtools/` exists** in the tree. `hack/dev/images-dev.yaml:5`
  explicitly confirms this: "ubuntu-devtools in the real catalog has
  no local manifest yet." This is the natural candidate for one of the
  "one or two additional apps" requested — it closes an already-announced gap
  instead of inventing a new one.
- **The current hardening is incompatible with sudo/dev usage as-is**,
  by design and not by oversight: `HARDENING.md` mandates
  "No setuid/setgid binaries: all `+s` bits stripped" and
  "Read-only rootfs compatible," verified by the CI smoke test
  (`--read-only --cap-drop ALL --security-opt no-new-privileges`,
  README.md:109-111). `sudo` is a setuid binary by nature and needs
  to write (apt) outside `/home/user`/`/tmp`/`/run` — the approach
  already used elsewhere (`RDP_AUTH_ENABLED`, a **runtime** flag that
  loosens a posture) doesn't apply here: you can't enable
  sudo after the fact on an image without the setuid binary or a
  writable rootfs. A "less hardened" variant is necessarily a
  **build-time choice**, not an environment flag — to be documented as
  such in part B.
- **The split touches real anchor points in the monorepo**,
  listed here to avoid a repeated audit in part A:
  - Root `.gitlab-ci.yml`: job `waas-images:` (`trigger: include:
waas-images/.gitlab-ci.yml`, stage `build`, triggered on
    `changes: [waas-images/**/*]`).
  - Root `Makefile:140-145` (`dev-build-images`): `$(MAKE) -C
waas-images build IMAGE=$$img` — this is the core of the
    `dev-bootstrap`/`dev-reload-all` loop delivered by
    `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md`. A
    naive split **silently breaks this loop**.
  - `release-please-config.json:7` already anticipates `waas-images/`
    leaving the monorepo ("waas-images/ is out of scope (own
    per-image versioning)") — the mention must be updated once
    the folder has physically left, not just left as-is.
  - Docs to update: `docs/ci.md` (lines 23, 57, 99, 143, 145),
    `docs/ci-github.md` (36, 90), `docs/governance.md:199`,
    root `README.md:51`, `hack/dev/images-dev.yaml:4-5`.
  - Code comments pointing to `waas-images` as a convention (no
    functional link, leave as-is or rephrase without removing them):
    `operator/api/v1alpha1/workspacetemplate_types.go:198,319,381`,
    `operator/api/v1alpha1/workspaceimage_types.go:54`.
  - **What must NOT be touched**: the image coordinates published
    in `gitops/governance/images.yaml` (`registry.xorhub.io/waas/waas-images/ubuntu-xfce:1.0.0`,
    etc.) — this is the address of an already-published artifact, independent
    of where the source tree lives. Renaming them is a separate,
    riskier decision (pinned tags), out of scope for this prompt.

## What needs to be delivered

### A. Split — to be executed in full (mandatory, first)

1. **Preserve history.** Use `git filter-repo` (not `git
subtree split`, slower and less reliable on a monorepo of this
   size) on a **throwaway clone** of the current repo, filtered on
   `waas-images/` with a `path-rename` that moves its content up to the
   root. Never touch the user's working clone for
   this step — operate in a temporary directory, then initialize
   the new repo at its final location.
2. **Location**: the new repo must end up at
   `../waas-image` — a directory sibling to the root of this monorepo
   (literal path requested by the user, singular, while
   the internal tree and all the documentation keep being called
   `waas-images` everywhere else: titles, already-published registry
   paths, comments). **Don't rename anything inside** the moved tree
   on this basis alone — document the choice (external
   singular folder, unchanged internal convention) as an open point
   rather than silently deciding one way or the other.
3. **Remove `waas-images/` from the monorepo** with a simple commit
   (`git rm -r waas-images`), **without rewriting the monorepo's
   history** — it keeps the memory of the folder, only the future
   changes.
4. **Fix every anchor point** listed above in "What already
   exists":
   - Root `.gitlab-ci.yml`: decide and implement (see "Open points")
     either the pure removal of the `waas-images:` job, or a
     cross-repo trigger (`include: project:, file:` / GitLab
     multi-project pipeline) pointing to the new repo.
   - Root `Makefile`: `dev-build-images` must resolve the new
     repo to a configurable path (`WAAS_IMAGES_DIR ?= ../waas-image`)
     and **fail with an actionable message** (not a cryptic make
     error) if the directory is absent — indicate the expected clone
     command in the error message rather than cloning on its
     own (a clone triggers network and disk writes not
     explicitly requested).
   - All the docs listed above (`docs/ci.md`, `docs/ci-github.md`,
     `docs/governance.md`, root `README.md`,
     `hack/dev/images-dev.yaml`, `release-please-config.json`).
5. **Bootstrap the new repo**: the existing README/CI/Makefile are
   sufficient as-is to get started (they're already self-contained —
   the `waas-images/` Makefile doesn't depend on anything outside its
   own folder). Add a license if the monorepo root has one and
   its type should propagate — verify before assuming.
6. **Don't push any new remote without explicit confirmation**:
   create the local `../waas-image` directory and its initial commit, but
   stop before any `git remote add` / `git push` to a forge
   — the target namespace (same GitLab org, new GitHub project,
   dual publication) is an open point, not a
   technical given.

### B. Study — documentation-only deliverable (in the new repo, `../waas-image/docs/RECIPE-STUDY.md`)

Write a study at the same level of rigor as
`docs/studies/kasm-images-feasibility.md` or
`docs/studies/protocol-feature-matrix-2026-07-10.md` in this monorepo
(verified findings, cited sources, an assumed but not
imposed recommendation — the final decisions belong to the user). Cover,
in this order:

1. **Recipe format simplification.** Does the current system
   (`images.yaml` + `manifest.yaml` + hand-written Dockerfile per node)
   already cover the "OS/applications/single-app cooking recipe in YAML"
   need, or is a declarative compiler
   (`recipe.yaml` → generated Dockerfile) missing on top of the `apps/` layer?
   If you propose this compiler, explicitly evaluate, without a
   reflexive dependency addition:
   - **cloud-init** — judge whether it has a real role at container
     image build-time (probably not: it's a VM/instance
     provisioning tool, not an OCI build tool); document the
     verdict instead of dismissing it without proof or adding it without
     justification.
   - **supervisord** — already the repo's runtime orchestrator; could
     a supervisord config fragment per app replace the Dockerfile for
     the "single app, no desktop" case?
   - **home-grown script** — consistent with the philosophy already in
     place (no new dependency if avoidable, cf. HARDENING.md): does a
     small Dockerfile-from-YAML generator do better than the 4
     manual steps currently in place (README.md:113-141) without
     reintroducing complexity that the current system already avoids?
     Recommend one option, with acknowledged trade-offs — not a neutral
     list of possibilities.
2. **GitHub Actions matrix CI**, mirroring the GitLab pipeline
   generated by `ci/generate_pipeline.py` (same topological sort
   base→desktop→apps, same smoke/trivy/cosign gates per image × arch).
   Close the gap already documented at `docs/ci-github.md:90` of this
   monorepo — check whether it needs to be duplicated/adapted in the new
   repo once separated. Propose the design (jobs, matrix, reuse
   or not of `generate_pipeline.py`); don't write the final YAML in
   this study, it will be delivered in the arbitrated follow-up prompt.
3. **New OS/applications**, one or two are enough, at least one new
   base OS:
   - **`ubuntu-devtools`** as priority — closes a gap already announced
     on the governance side (`gitops/governance/images.yaml:53-70`,
     `allowedGroups: [nymphe:dev]` restriction already written) and already
     flagged as missing (`hack/dev/images-dev.yaml:5`), rather than a
     speculative proposal.
   - One additional application of your choice (justify it: real
     usefulness for a WaaS workstation, reasonable addition complexity —
     code-server/VS Code web and a second browser are plausible
     leads, no obligation).
   - A new base OS: evaluate the real delta from
     `base/ubuntu/Dockerfile` for the proposed candidate (Debian being the
     technically closest, apt-based like Ubuntu) — only propose it
     if the real maintenance cost (equivalent packages, xrdp,
     packaged TigerVNC) has been verified, not assumed.
4. **Hardened/less-hardened build-time variant**, for
   development-dedicated environments where the user needs
   `apt install`/sudo in-session. As established above, this can't
   be a runtime flag — propose the build mechanism (new
   build-arg like `INSTALL_SUDO=1`, distinct `-dev` tag, sudoers
   NOPASSWD for UID 1000, non-read-only rootfs for that specific
   tag) and its registration in `HARDENING.md` as a **reduced and
   documented** security profile, not a silent regression
   of the current checklist. Also propose how to prevent it from accidentally
   leaking to the general population — the repo already has a usable
   precedent (`allowedGroups` on `ubuntu-devtools`,
   `gitops/governance/images.yaml:64-66`).

End the study with an **"Open points"** section that restates,
as an arbitrable list, each of the choices above (recipe
format, cloud-init inclusion, GitHub Actions target, candidate OS,
2nd candidate app, exact mechanism of the dev variant) — this is what
the user will arbitrate before the follow-up prompt.

## Constraints to respect

- Part A must leave both the monorepo and the new repo
  in a state that builds/lints without error: no CI job broken by an
  `include` pointing to a path that no longer exists, no Makefile
  target that fails silently (`No rule to make target`).
- Part B modifies no Dockerfile, no manifest, no CI pipeline — text
  only. Resist the temptation to implement a proposal "while you're
  at it" — it isn't arbitrated.
- Don't rename or move the already-published registry coordinates
  (`gitops/governance/images.yaml`) in this prompt.
- No `git push` to a new remote without explicit confirmation
  from the user.
- Any part A decision that has a reasonable alternative
  (removal vs. cross-repo trigger of the GitLab job; singular
  naming of the external folder vs. internal convention) must be
  documented in the commit message or the study, not just silently
  decided.

## Open points (your arbitration)

- Pure removal of the `waas-images:` job in the root
  `.gitlab-ci.yml` vs. cross-repo trigger to the now-external
  `../waas-image` — depends on the target forge/organization, to be confirmed
  before coding.
- Namespace of the new remote (same GitLab org, new GitHub project,
  dual publication) — conditions the answer to part B's GitHub
  Actions CI question.
- Name of the external folder (`waas-image` singular, requested as-is) vs.
  unchanged internal convention (`waas-images` everywhere in the code/doc
  of the moved tree) — to be assumed explicitly, not standardized
  without saying so. Arbitration: the waas-images folder already exists and
  already has a git init done at the path ~/Documents/Personal/Projects/XorHub/waas-images/
- Recipe compiler (cloud-init/supervisord/home-grown script) — your
  judgment in study B.1, not a technical given.
- Exact mechanism of the dev/less-hardened variant (precise build-arg,
  tag name, catalog gating) — your judgment in study B.4.
</content>
