#!/bin/sh
# audit-orphans.sh — inventory (and optionally clean) WaaS resources whose
# parent Workspace no longer exists.
#
# What it checks, cluster-wide:
#   1. Namespaced objects labeled app.kubernetes.io/managed-by=waas-operator
#      carrying a waas.xorhub.io/workspace label that points to a Workspace
#      CR that is gone. The type list is NOT hardcoded: every listable
#      namespaced type is swept, so new resource kinds are covered
#      automatically.
#   2. Operator-created namespaces (managed-by label) that hold no waas
#      object anymore:
#        - cleanup=DeleteWhenEmpty  -> the janitor should have reclaimed it
#                                      (drift: report, clean with --clean)
#        - no cleanup label         -> created before the policy was frozen
#                                      on namespaces; needs a HUMAN decision
#                                      (reported, never auto-deleted)
#   3. Retained home volumes (informational: they are supposed to survive).
#
# Open database sessions are swept server-side by the api-server's session
# sweeper (WAAS_SESSION_SWEEP_INTERVAL); they are not visible to kubectl.
#
# Usage:
#   hack/audit-orphans.sh [--clean] [--platform-namespace NS]
#     --clean   delete category 1 orphans and category 2 DeleteWhenEmpty
#               namespaces. Retained volumes and unlabeled namespaces are
#               NEVER deleted by this script.
set -eu

CLEAN=false
while [ $# -gt 0 ]; do
    case "$1" in
        --clean) CLEAN=true ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
    shift
done

MANAGED="app.kubernetes.io/managed-by=waas-operator"
WS_LABEL="waas.xorhub.io/workspace"
RETAINED_LABEL="waas.xorhub.io/retained"

# Live workspaces as "<cr-namespace>/<cr-name>" lines.
live_workspaces=$(kubectl get workspaces -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null || true)

is_live() { # $1 = cr-ns/cr-name
    printf '%s\n' "$live_workspaces" | grep -qxF "$1"
}

echo "==> [1/3] Managed objects whose Workspace is gone (no output = clean)"
# Every listable namespaced type; failures on exotic types are ignored.
types=$(kubectl api-resources --verbs=list --namespaced -o name | sort -u)
for type in $types; do
    kubectl get "$type" -A -l "$MANAGED,$WS_LABEL" \
        -o jsonpath="{range .items[*]}{.metadata.namespace}{'\t'}{.metadata.name}{'\t'}{.metadata.labels['waas\.xorhub\.io/workspace']}{'\t'}{.metadata.labels['waas\.xorhub\.io/workspace-namespace']}{'\n'}{end}" \
        2>/dev/null | while IFS="$(printf '\t')" read -r ns name ws wsns; do
        [ -n "$name" ] || continue
        # Legacy objects without the CR-namespace label sit beside their CR.
        [ -n "$wsns" ] || wsns="$ns"
        if ! is_live "$wsns/$ws"; then
            echo "  ORPHAN $type $ns/$name (workspace $wsns/$ws is gone)"
            if [ "$CLEAN" = true ]; then
                kubectl delete "$type" -n "$ns" "$name" --wait=false
            fi
        fi
    done || true
done

echo "==> [2/3] Operator-created namespaces without waas content"
kubectl get namespaces -l "$MANAGED" \
    -o jsonpath="{range .items[*]}{.metadata.name}{'\t'}{.metadata.labels['waas\.xorhub\.io/cleanup']}{'\n'}{end}" \
    | while IFS="$(printf '\t')" read -r ns policy; do
    [ -n "$ns" ] || continue
    # Anything waas-managed still inside (retained volumes included)?
    content=$(kubectl get deploy,sts,pod,svc,pvc -n "$ns" -l "$MANAGED" -o name 2>/dev/null | head -1)
    # A workspace still targeting it?
    targeting=$(kubectl get workspaces -A -o jsonpath="{range .items[?(@.spec.targetNamespace=='$ns')]}{.metadata.name}{end}" 2>/dev/null)
    [ -z "$content" ] && [ -z "$targeting" ] || continue
    case "$policy" in
        DeleteWhenEmpty)
            echo "  EMPTY $ns (DeleteWhenEmpty — the janitor should reclaim it; drift if it persists)"
            if [ "$CLEAN" = true ]; then
                kubectl delete namespace "$ns" --wait=false
            fi
            ;;
        Retain)
            echo "  empty $ns (Retain — kept by policy, nothing to do)"
            ;;
        *)
            echo "  EMPTY $ns (no cleanup label: pre-migration namespace; decide manually:"
            echo "         kubectl delete namespace $ns   # if it was per-workspace/per-user and is no longer wanted)"
            ;;
    esac
done

echo "==> [3/3] Retained home volumes (kept on purpose, shown for visibility)"
kubectl get pvc -A -l "$RETAINED_LABEL=true" \
    -o custom-columns="NS:.metadata.namespace,NAME:.metadata.name,OWNER:.metadata.labels.waas\.xorhub\.io/owner,ORIGIN:.metadata.annotations.waas\.xorhub\.io/origin-workspace,SINCE:.metadata.annotations.waas\.xorhub\.io/retained-at" \
    2>/dev/null || echo "  (none)"

if [ "$CLEAN" = true ]; then
    echo "==> clean pass done (deletions issued with --wait=false; re-run without --clean to verify)"
fi
