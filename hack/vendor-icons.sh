#!/bin/sh
# vendor-icons.sh — refresh the two vendored OS-fallback icons.
#
# App icons are NOT vendored anymore: the portal loads them live from
# the dashboard-icons CDN (resolveIcon in frontend/src/lib/icon.ts).
# Only the two OS fallbacks — shown when a slug is absent/invalid or
# the CDN load fails — are committed under frontend/public/icons/,
# downloaded here from the same CDN (github.com/homarr-labs/
# dashboard-icons, Apache-2.0 — see frontend/public/icons/
# ATTRIBUTION.md). This script is a maintainer refresh tool, NEVER
# executed at build or runtime.

set -eu

CDN="https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg"
DEST="$(dirname "$0")/../frontend/public/icons"

mkdir -p "$DEST"

# Renamed on purpose so an "os-*" file can never collide with an app
# slug ("windows"/"ubuntu" are not dashboard-icons slugs anyway — the
# real ones are "microsoft-windows"/"ubuntu-linux").
echo "  linux -> os-linux"
curl -fsSL "$CDN/linux.svg" -o "$DEST/os-linux.svg"
echo "  microsoft-windows -> os-windows"
curl -fsSL "$CDN/microsoft-windows.svg" -o "$DEST/os-windows.svg"

set -- "$DEST"/*.svg
echo "done: $# icons in $DEST"
