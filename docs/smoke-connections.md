# Per-protocol connection test (delivery gate)

`test/smoke` establishes a **real** guacd session for each protocol
(vnc, rdp, ssh) through the full stack — public API, operator,
placement in a dedicated namespace, wwt, guacd, desktop image. It
exists because "the workspace is Ready" proves nothing about the
session path: a NetworkPolicy that rejects guacd, a fake
`Status.Address` or broken credentials all pass readiness and only
die at connect time — exactly the "connection closed" regression
from July 2026 (see `docs/diagnostics/placed-namespace-netpol.md`).

## What the test does, per protocol

1. log in on the public API (validation account);
2. pick a template: the first catalog template serving the protocol
   (preferring the template for which it's the default protocol);
3. create a workspace, wait for phase `Running` — if the
   template has a downtime cron and the workspace is born `Stopped`,
   the test does what the portal does: a `resume`, then waits;
4. `POST /connect {protocol}` then open the wwt WebSocket with the
   connection token;
5. read the guacd stream: **success on the first `sync`
   instruction** (guacd's protocol client actually reached the desktop
   and pushed a frame); failure on an `error`/`disconnect`
   instruction, or the stream closing. An open socket isn't enough:
   guacd only opens the connection to the desktop after its handshake
   with wwt;
6. delete the workspace (always, even on failure).

## Running it

```sh
# against the dev k3d environment (default URL and credentials):
make smoke

# against another environment:
WAAS_SMOKE_URL=https://waas.example.com \
WAAS_SMOKE_USER=validation WAAS_SMOKE_PASSWORD=... \
go test -count=1 -v ./test/smoke/
```

Variables: `WAAS_SMOKE_URL` (without it the test **skips** — `go test
./...` stays usable offline), `WAAS_SMOKE_USER`/`WAAS_SMOKE_PASSWORD`
(default dev admin/admin123), `WAAS_SMOKE_PROTOCOLS` (default
`vnc,rdp,ssh`), `WAAS_SMOKE_READY_TIMEOUT` (default 5m),
`WAAS_SMOKE_PLATFORM_NAMESPACE` (default `waas` — see below).

## `vnc-audio` subtest: the PulseAudio port (4713)

A live VNC session proves nothing about the audio port: guacd
composes it separately, and a Service missing the port fails
silently (session OK, no sound). When a template exposes the audio port
(`protocols[].exposeAudioPort`, seeded in dev by `ubuntu-firefox` — a
browser also being the natural manual test: play a video), the
`vnc-audio` subtest establishes the session then verifies that
`<service>:4713` responds over TCP **from the platform namespace** — the
exact path guacd takes, default-deny NetworkPolicy included. The probe
is an ephemeral `kubectl run` pod (busybox `nc -z`): unlike the rest of
the smoke test, this subtest needs `kubectl` in the PATH (skipped
otherwise, same as when no template exposes the port).

## Delivery criterion

An iteration is not shippable if `make smoke` doesn't pass on
the validation environment. CI integration (GitLab): a `validate`
stage job that spins up the ephemeral k3d (`make dev-bootstrap`) then
runs `make smoke`. This is the lightest setup
that stays reliable: it uses exactly the browser's path (same
ingress, same WebSocket), without a browser or Selenium.

The validation environment's catalog must cover every
protocol (the test fails if a protocol has no template — this is
intentional: a catalog that loses a protocol is a regression).
