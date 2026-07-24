# Changelog

## [0.3.0](https://github.com/XoRHub/waas/compare/chart-0.2.0...chart-0.3.0) (2026-07-24)


### ⚠ BREAKING CHANGES

* drop the v prefix from appVersion and promoted image tags

### ci

* drop the v prefix from appVersion and promoted image tags ([d33c6f9](https://github.com/XoRHub/waas/commit/d33c6f9e5c60d8aac7e93083afc30a8eae759443))


### Chores

* **deps:** update bitnami/kubectl:latest docker digest to 95de17e ([8903648](https://github.com/XoRHub/waas/commit/8903648f6e64689c06eb6b6f3215621464d2cb84))
* **deps:** update bitnami/kubectl:latest docker digest to 95de17e ([f558af9](https://github.com/XoRHub/waas/commit/f558af95cf2767e2935131476156235436dce8db))

## [0.2.0](https://github.com/XoRHub/waas/compare/waas-chart-0.1.0...waas-chart-0.2.0) (2026-07-19)


### ⚠ BREAKING CHANGES

* **operator:** desktop password env becomes WAAS_DESKTOP_PASSWORD

### Features

* **api-server:** OIDC login, effective-policy debug, registry validation ([7d75d74](https://github.com/XoRHub/waas/commit/7d75d74d36001d256ef128d2390952a6df327927))
* **api:** Remote Workspaces — out-of-cluster machines via guacd ([6358d19](https://github.com/XoRHub/waas/commit/6358d193527a9d7a2fb0d8f6fb36b022965371ea))
* **audio:** expose the VNC PulseAudio port (4713) end to end ([85a22cb](https://github.com/XoRHub/waas/commit/85a22cbc3dc22d7d9b9d7fc898ae500508a539a3))
* **auth:** let deployments disable local login once OIDC is configured ([8aa5934](https://github.com/XoRHub/waas/commit/8aa5934e22a2d5c5c505331a96da167924c7e743))
* **catalog:** private-registry pull secrets propagated to workspaces ([d22b869](https://github.com/XoRHub/waas/commit/d22b8693d1f14759387e5a608068ea1c9f377e0a))
* **catalog:** registry entries and tag-pinning discipline ([4f93739](https://github.com/XoRHub/waas/commit/4f9373936e73f6c61fe2b13c0c5815f462a08200))
* **credentials:** generate per-workspace vnc/rdp passwords, default waas_user ([5d0ba29](https://github.com/XoRHub/waas/commit/5d0ba296fcf6fc53d14ddc1d0e2602ff0ef00bf0))
* cron-based uptime/downtime scheduling for workspaces (conflict rule B) ([59f7c47](https://github.com/XoRHub/waas/commit/59f7c479a7e56cae3f216aec455be6f146df66a1))
* **events:** ArgoCD-style workspace events panel ([abb667b](https://github.com/XoRHub/waas/commit/abb667b286b12d26dcc82197c2a4a2ad42536f04))
* **events:** live updates for templates, catalog, policies and volumes ([a7bca40](https://github.com/XoRHub/waas/commit/a7bca409798afde84e861fabe1acc6c5fc20cacc))
* **frontend:** show the resolved namespace (creation, fleet) + pattern editor with hints ([5c6e600](https://github.com/XoRHub/waas/commit/5c6e60017c1afac792393b5f63479ea1dddf547d))
* **governance:** explicit all-rights admins policy bootstrap ([0ea0ea9](https://github.com/XoRHub/waas/commit/0ea0ea9b51445d67ef9d8e16eb1a5145b16a29bb))
* **helm:** add default waas catalgby default and optional kasm catalog ([de89179](https://github.com/XoRHub/waas/commit/de891796afb101ddeb7a349876b4fb2505b49757))
* **helm:** add missing spec in GrafnaDashboard ([78dcbdf](https://github.com/XoRHub/waas/commit/78dcbdfa9e2f097994c77ac09a509d425cf5ba94))
* **helm:** add ssh protocol to waas catalog ([67a2c31](https://github.com/XoRHub/waas/commit/67a2c312afc922ed477edea520335e241c527746))
* **helm:** add support for httpRoute ([02315d0](https://github.com/XoRHub/waas/commit/02315d0f20858206e3c57b145b2350cbe7724dea))
* **helm:** expose full WorkspacePolicy spec on admin/default policies, rename OIDC client secret to clientSecretRef ([1359c24](https://github.com/XoRHub/waas/commit/1359c24a4059dff105338e3a8647f76df00d8cf3))
* **helm:** generate chart README with helm-docs, add helm-unittest suites and CI gates ([0fed38a](https://github.com/XoRHub/waas/commit/0fed38af35740dc5baab1c278804996b68be60bd))
* **helm:** Grafana dashboards, configmap and grafana-operator modes ([5ade91f](https://github.com/XoRHub/waas/commit/5ade91fc9ff072411150a671b128b292b7c2e114))
* **helm:** maxRunningWorkspaces in bootstrap policies, null limits pruned from the render ([1cc895b](https://github.com/XoRHub/waas/commit/1cc895b4340ffb873ce77d54b0e801164c8ed21a))
* **helm:** opt-in metrics — scrape annotations, PodMonitor, ServiceMonitor ([ff155c2](https://github.com/XoRHub/waas/commit/ff155c2c810ec7576ae9f201b4aaecaa1d5ff1fd))
* **helm:** RBAC and env wiring for workspace governance ([242e114](https://github.com/XoRHub/waas/commit/242e11453d4edf3bf3dac4505939347ca6f83ace))
* **helm:** rename operator.catalogSyncInterval to apiServer.catalogSyncInterval ([503ae6d](https://github.com/XoRHub/waas/commit/503ae6d7dea719ed9dc200c487713e10ad928889))
* **helm:** single-chart install for operator, api-server, wwt, frontend, guacd and postgres ([9234047](https://github.com/XoRHub/waas/commit/92340472fbcd6d50156ec7de93c4059e3279f5cc))
* **helm:** source internal-token from an existing secret via internalTokenSecretRef ([27fb115](https://github.com/XoRHub/waas/commit/27fb1153e8679d928a33c35c80a9ddf0339a6bb0))
* **helm:** source postgres/admin/OIDC secrets per-field and make the secrets-job hook disableable ([72b6b20](https://github.com/XoRHub/waas/commit/72b6b2018c0d17b070e80b9e48ad3f4186b1bf26))
* **helm:** split JWT signing key into its own job, add global/pod labels-annotations and a default policy ([001e88c](https://github.com/XoRHub/waas/commit/001e88cea31e4e4e325081d0fc2afcbe4ff717fc))
* home volume retention — choice at deletion, dashboards, quota ([a66282f](https://github.com/XoRHub/waas/commit/a66282fed1ebd560d4356cf803b908df5417cb5a))
* **kasmvnc:** govern the config — policy-enforced clipboard DLP and admin-managed kasmvnc.yaml ([e39d092](https://github.com/XoRHub/waas/commit/e39d092c34f56b3f37039750a680e56cab7c6f3f))
* **kasmvnc:** phase 1 data plane — wwt reverse proxy, iframe client ([482c705](https://github.com/XoRHub/waas/commit/482c7058f1682f8cbbc23acdd57f4a30568aa753))
* **kasmvnc:** phase 2 — CRDs, generated VNC_PW secret, seeds, smoke ([4d7100f](https://github.com/XoRHub/waas/commit/4d7100fa4106ff4154b3eccb9c2cc59e013516fe))
* **kasmvnc:** show users the applied config read-only instead of 'no tunable parameters' ([28befe6](https://github.com/XoRHub/waas/commit/28befe6c4df48127a169a3ad05c51dccaae694d8))
* **operator:** add spec.catalog and status.catalog to WorkspaceImage ([72cdea0](https://github.com/XoRHub/waas/commit/72cdea0dbc8d3d65d8dd3a7e3cd134b349ed13ac))
* **operator:** add WorkspaceTemplate spec.logo for a configurable portal icon ([637359e](https://github.com/XoRHub/waas/commit/637359e5d2d8412e22572a824e6403d6c565869a))
* **operator:** dedicated-namespace workload placement, frozen naming, custom metadata ([0a47b95](https://github.com/XoRHub/waas/commit/0a47b95ec1a6700adfbe56c90437344a605b3996))
* **operator:** desktop password env becomes WAAS_DESKTOP_PASSWORD ([3eb984c](https://github.com/XoRHub/waas/commit/3eb984c02f70a2ac2ac634850b4b962d5d9ca6a7))
* **operator:** global namespace pattern (WAAS_DEFAULT_NAMESPACE_PATTERN) + {templateName}/{os} placeholders ([ea569ff](https://github.com/XoRHub/waas/commit/ea569ff36a0b19017a7f66e35e41bd7796b3c894))
* **operator:** guacd params registry, template webhook, clipboard policy ([451d6df](https://github.com/XoRHub/waas/commit/451d6df34b5f6a7c90c7fa9a258d777364c70942))
* **operator:** homeVolume template metadata on the home PVC ([68b609b](https://github.com/XoRHub/waas/commit/68b609b37a76dce3955df4395f63d45da0362d17))
* **operator:** maxRunningWorkspaces limit caps concurrently running workspaces per user ([c6fc3af](https://github.com/XoRHub/waas/commit/c6fc3af3c7a9446216d899dd35b8a71036862e26))
* **operator:** pause = scale-to-0 with a Paused phase distinct from Stopped ([3ccbb90](https://github.com/XoRHub/waas/commit/3ccbb90f25264c6cd6fef1859658714ad515b4d9))
* **operator:** policy — overrides allowlist + opt-in remoteWorkspaces ([8aeeb33](https://github.com/XoRHub/waas/commit/8aeeb33c75a4fa4b54bb231b21e09415b5d83385))
* **operator:** remove the catalog reconciler and status.catalog.entries ([f0a00dd](https://github.com/XoRHub/waas/commit/f0a00dd53d1ebdd4d71b208a18e07115e608f7b1))
* **operator:** sync catalog manifests into WorkspaceImage status ([600bb17](https://github.com/XoRHub/waas/commit/600bb17bb639fff5a7e9acc97e036a8f5573091a))
* **operator:** template edits converge at scale-up boundaries, drift surfaced end to end ([27bba2f](https://github.com/XoRHub/waas/commit/27bba2fa9f7283789ff9771b8f575d4e73e68360))
* **operator:** workload kinds, multi-protocol templates and creator overrides ([17d04ae](https://github.com/XoRHub/waas/commit/17d04ae67d4a29ebaab7532efceb27436466004d))
* **operator:** Workspace and WorkspaceTemplate CRDs with reconciler and KubeVirt-aware webhook ([ef0c5f5](https://github.com/XoRHub/waas/commit/ef0c5f5dc8dac26da148c590b1d3d5d20a74ba6d))
* **params:** delegate userParams by category and gate /connect on the template right ([5b1f1cf](https://github.com/XoRHub/waas/commit/5b1f1cf8073f4edad307f9cbfc91581eb3ca8035))
* **resize:** real end-to-end VNC/RDP session resize via pod exec ([906017f](https://github.com/XoRHub/waas/commit/906017f3272e823f681aeec793cf90414394e246))
* **status:** ConnectionReady condition, Ready printcolumn, condition generations ([3574b32](https://github.com/XoRHub/waas/commit/3574b3232b9ad2e76142381b920052e2461cea10))
* **template:** opaque KasmVNC user config mounted at ~/.vnc/kasmvnc.yaml ([78dd244](https://github.com/XoRHub/waas/commit/78dd2447557c1ae071ac4994d34feb759cd2057b))
* Wake-on-LAN for remote workspaces via an external relay ([d4376ee](https://github.com/XoRHub/waas/commit/d4376eeebd37a07042d803e8291749d2e648b37b))


### Bug Fixes

* **crds:** drift based on sigs.k8s.io controller ([bb03e10](https://github.com/XoRHub/waas/commit/bb03e100554fd42a034504445e5909f5d2ba4beb))
* **deps:** update go-non-major ([7b70fd6](https://github.com/XoRHub/waas/commit/7b70fd6939d792d63edcee9d30d17ba8592d1343))
* **helm:** align tag policy with kasm-catalog ([8fabb9c](https://github.com/XoRHub/waas/commit/8fabb9c3a1ca3862a79968fad044691e711e68be))
* **helm:** audit-2 order 5 — in-cluster secret bootstrap, no lookup (C13) ([052ce3f](https://github.com/XoRHub/waas/commit/052ce3f86548830d18be9cebcd38529722461b53))
* **helm:** declare the waasImages catalog tagPolicy in values ([86f7d96](https://github.com/XoRHub/waas/commit/86f7d96d23a1cca2392915117fab6b4410b11391))
* **helm:** drop the operator's residual workspaceimages/status grant ([51d0854](https://github.com/XoRHub/waas/commit/51d0854c060b2e65e039d05eb4ae426a9591ac2a))
* **lifecycle:** complete teardown on workspace deletion ([d3cf422](https://github.com/XoRHub/waas/commit/d3cf4223c7debe03e4236089eb78fe534e70da86))
* **operator:** avoid gofmt doc-comment quote mangling in a CEL rule ([52ca39a](https://github.com/XoRHub/waas/commit/52ca39a52ddaeb00e7b9c177165772c2f7dd0ef4))
* **operator:** placed-namespace netpol must let guacd/wwt in ([cb3b34f](https://github.com/XoRHub/waas/commit/cb3b34fd76081b8ba5b9de545517927aa8437f1c))
* **operator:** rbac update verb on workloads — pause/resume and crons weren't scaling ([94ceafa](https://github.com/XoRHub/waas/commit/94ceafa9a190cbbc9f312b678757bdb8bcbea732))
* **operator:** VNC/RDP regression — placed-namespace netpol now self-heals ([8f4c952](https://github.com/XoRHub/waas/commit/8f4c9525f60ff3e0a307ea4e7b823adbf1c82f60))
* **webhook:** reject templates combining kasmvnc with guacd protocols ([4edf114](https://github.com/XoRHub/waas/commit/4edf1144156aa4b2c5c9fc05e3b4723197dbc1f8))


### Chores

* **deps:** update module sigs.k8s.io/controller-tools to v0.21.0 ([4af32c0](https://github.com/XoRHub/waas/commit/4af32c09bbc115037b39d1ec8fab01c92f262bc2))
* **helm:** pin postgres to 17.10-alpine by digest ([280d895](https://github.com/XoRHub/waas/commit/280d895bd1b3d58ab0476749eb89ea001fff7923))
* **main:** release 0.2.0 ([fdb3bf5](https://github.com/XoRHub/waas/commit/fdb3bf5e1cc47b2dd5f8bcc3fb101008b22e3d94))
* **main:** release 0.2.0 ([3659f84](https://github.com/XoRHub/waas/commit/3659f84490a92fd0885efe512a84a80c3e410d96))
* **operator:** regenerate CRDs and schemas for k8s.io 0.36 ([31b464b](https://github.com/XoRHub/waas/commit/31b464b6ac3225721c0efaddd1620050e0dac764))
