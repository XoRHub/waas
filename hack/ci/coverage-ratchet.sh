#!/usr/bin/env bash
# Per-package coverage ratchet: fails when a package drops below its
# floor. Reads a merged -coverpkg profile, where every test binary emits
# blocks for EVERY package — the same block appears once per binary, so
# blocks must be deduplicated (counted once, covered if ANY binary hit
# them) before aggregating per package.
#
# Usage: coverage-ratchet.sh <profile> <package-suffix>:<min-pct> [...]
#   e.g. coverage-ratchet.sh coverage.out internal/handler:40 internal/repository:50
set -euo pipefail

profile=$1
shift

fail=0
for spec in "$@"; do
  pkg=${spec%:*}
  min=${spec##*:}
  pct=$(awk -v pkg="$pkg/" '
    NR > 1 {
      split($0, f, " ")
      if (index(f[1], pkg) == 0) next
      nstmts[f[1]] = f[2]
      if (f[3] > 0) hit[f[1]] = 1
    }
    END {
      for (b in nstmts) { total += nstmts[b]; if (hit[b]) covered += nstmts[b] }
      if (total == 0) print "0.0"; else printf "%.1f", covered * 100 / total
    }
  ' "$profile")
  if awk -v p="$pct" -v m="$min" 'BEGIN { exit !(p >= m) }'; then
    echo "ok   ${pkg}: ${pct}% (floor ${min}%)"
  else
    echo "FAIL ${pkg}: ${pct}% is below the ${min}% floor"
    fail=1
  fi
done
exit $fail
