#!/bin/sh
# vendor-icons.sh — refresh the vendored dashboard-icons subset.
#
# Downloads the SVG logos referenced by the known image catalogs
# (ghcr.io/xorhub/waas-images, docker.io/kasmweb) plus the two OS
# fallbacks into frontend/public/icons/, from the dashboard-icons CDN
# (github.com/homarr-labs/dashboard-icons, Apache-2.0 — see
# frontend/public/icons/ATTRIBUTION.md). Icons are committed: this
# script is a maintainer refresh tool, NEVER executed at build or
# runtime — the app only serves the local copies (resolveIcon in
# frontend/src/lib/icon.ts, no network fetch).
#
# To add an icon: append its dashboard-icons slug to SLUGS below (check
# https://dashboardicons.com first — e.g. "windows"/"ubuntu" do NOT
# exist, the real slugs are "microsoft-windows"/"ubuntu-linux"), rerun,
# commit the new SVG.

set -eu

CDN="https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg"
DEST="$(dirname "$0")/../frontend/public/icons"

# App slugs referenced by the two known catalogs.
SLUGS="firefox google-chrome chromium kasm terminal code ubuntu-linux"

mkdir -p "$DEST"
for slug in $SLUGS; do
  echo "  $slug"
  curl -fsSL "$CDN/$slug.svg" -o "$DEST/$slug.svg"
done

# OS fallbacks (resolveIcon falls back to os-<linux|windows>.svg when a
# slug is absent or not vendored); renamed on purpose so an "os-*" file
# can never collide with an app slug.
echo "  linux -> os-linux"
curl -fsSL "$CDN/linux.svg" -o "$DEST/os-linux.svg"
echo "  microsoft-windows -> os-windows"
curl -fsSL "$CDN/microsoft-windows.svg" -o "$DEST/os-windows.svg"

set -- "$DEST"/*.svg
echo "done: $# icons in $DEST"
