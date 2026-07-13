# Icon attribution

All icons shown by the portal come from
[dashboard-icons](https://github.com/homarr-labs/dashboard-icons)
(homarr-labs), licensed under the
[Apache License 2.0](https://github.com/homarr-labs/dashboard-icons/blob/main/LICENSE)
— both the two SVG files vendored in this directory and the app icons
loaded at runtime from their CDN (`cdn.jsdelivr.net`).

Only the OS fallbacks are vendored and committed: `os-linux.svg` and
`os-windows.svg` are the dashboard-icons `linux` and
`microsoft-windows` icons, renamed, shown when a catalog entry has no
usable icon slug or the CDN load fails (see
`frontend/src/lib/icon.ts`). To refresh them, run
`hack/vendor-icons.sh` from the repo root and commit the result.
