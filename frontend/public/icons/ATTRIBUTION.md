# Icon attribution

The SVG files in this directory are a vendored subset of
[dashboard-icons](https://github.com/homarr-labs/dashboard-icons)
(homarr-labs), licensed under the
[Apache License 2.0](https://github.com/homarr-labs/dashboard-icons/blob/main/LICENSE).

They are served locally by the portal (no runtime fetch). To refresh or
extend the subset, run `hack/vendor-icons.sh` from the repo root and
commit the result. `os-linux.svg` and `os-windows.svg` are the
dashboard-icons `linux` and `microsoft-windows` icons, renamed to serve
as OS fallbacks (see `frontend/src/lib/icon.ts`).
