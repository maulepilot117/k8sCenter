#!/usr/bin/env sh
# scripts/check-cluster-routing.sh
#
# Phase 2 (Finding P2-5) — prevent regressions where handlers call
# K8sClient.ClientForUser / DynamicClientForUser directly, bypassing
# ClusterRouter.RouterFor and silently routing remote-cluster requests
# to the local cluster.
#
# Release C (U9b, plan D6) — in the packages listed in SCHEMA_ROUTED_DIRS,
# also flag .RESTMapper() / .DiscoveryClient(). Those return the LOCAL
# cluster's schema; pairing it with a remote client is the bug
# ClusterRouter.TargetFor / TargetSchemaFor exist to prevent.
#
# A line is exempt when:
#   - The line above carries `// nolint:cluster-routing` AND a free-form reason
#   - The file path matches one of the ALLOWED_PREFIXES below (informer
#     setup, prober, the LocalFactory() implementation in cluster_router.go,
#     client.go where the methods are defined)
#
# Usage:
#   bash scripts/check-cluster-routing.sh
#     -- warn mode when CHECK_CLUSTER_ROUTING_GATE=warn (default bootstrap);
#        prints all violations but exits 0.
#   CHECK_CLUSTER_ROUTING_GATE=fail bash scripts/check-cluster-routing.sh
#     -- strict mode; exits non-zero when any unexempt violation is found.
#
# Exits non-zero (strict mode only); always prints every violation before
# exiting so CI surfaces all sites in one run.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Directories under $ROOT to scan for direct K8sClient calls.
# Space-separated; each entry is processed in turn.
#
# Keep this list a superset of every package that exposes HTTP handlers OR
# wires per-request k8s calls. F#14 added server / alerting / gateway /
# notification / storage / velero — each one had at least one direct
# .ClientForUser call that the previous list missed.
HANDLER_DIRS="backend/internal/yaml backend/internal/k8s backend/internal/certmanager backend/internal/networking backend/internal/servicemesh backend/internal/gitops backend/internal/policy backend/internal/externalsecrets backend/internal/monitoring backend/internal/loki backend/internal/topology backend/internal/server backend/internal/alerting backend/internal/gateway backend/internal/notification backend/internal/storage backend/internal/velero"

# File paths (relative to ROOT, prefix-matched) whose direct calls are
# architecturally legitimate and therefore exempt from the lint:
#
#   cluster_router.go  — LocalFactory() wrapper IS the canonical call site
#   client.go          — method definitions live here
#   informers          — informer setup fetches clients at startup, not per-request
#   cluster_prober.go  — background goroutine; no per-request user context
ALLOWED_PREFIXES="backend/internal/k8s/cluster_router.go backend/internal/k8s/client.go backend/internal/k8s/informers backend/internal/k8s/cluster_prober.go"

# Directories (relative to ROOT) whose handlers resolve discovery and
# RESTMappers through ClusterRouter.TargetFor / TargetSchemaFor. In these, a
# .RESTMapper() or .DiscoveryClient() call is a violation too. A package
# joins this list when it migrates to per-target schema; until then its
# direct calls are deliberate local-cluster reads (the CRD-discovery caches
# in certmanager, gitops, policy and friends, for example) and flagging them
# would bury the real regressions.
SCHEMA_ROUTED_DIRS="backend/internal/yaml backend/internal/server"

# -----------------------------------------------------------------------
# Helpers
# -----------------------------------------------------------------------

# is_allowed_path PATH — returns 0 (true) if PATH starts with any allowed prefix.
is_allowed_path() {
  _p="$1"
  for _pfx in $ALLOWED_PREFIXES; do
    case "$_p" in
      "$_pfx"*) return 0 ;;
    esac
  done
  return 1
}

# nolint_on_prev_line FILE LINENO — returns 0 if the line above LINENO
# contains `// nolint:cluster-routing` followed by at least one non-space char.
nolint_on_prev_line() {
  _file="$1"
  _lineno="$2"
  _prev=$(( _lineno - 1 ))
  [ "$_prev" -lt 1 ] && return 1
  _prevline=$(sed -n "${_prev}p" "$_file")
  case "$_prevline" in
    *"// nolint:cluster-routing "*) return 0 ;;
  esac
  return 1
}

# is_schema_routed PATH — returns 0 (true) if PATH is inside a
# SCHEMA_ROUTED_DIRS entry.
is_schema_routed() {
  _p="$1"
  for _dir in $SCHEMA_ROUTED_DIRS; do
    case "$_p" in
      "$_dir"/*) return 0 ;;
    esac
  done
  return 1
}

# scan_file FILE — writes violation lines to stdout.
# Each violation is two lines: "VIOLATION rel/path:N" then "  <source line>".
scan_file() {
  _abs="$1"
  _rel="${_abs#"$ROOT/"}"

  is_allowed_path "$_rel" && return 0
  _schema=0
  is_schema_routed "$_rel" && _schema=1

  _lineno=0
  while IFS= read -r _line; do
    _lineno=$(( _lineno + 1 ))

    _hit=0
    case "$_line" in
      *".ClientForUser("*|*".DynamicClientForUser("*) _hit=1 ;;
      *".RESTMapper()"*|*".DiscoveryClient()"*)
        if [ "$_schema" -eq 1 ]; then _hit=1; fi ;;
    esac
    [ "$_hit" -eq 1 ] || continue

    # Skip lines that are interface / type / comment definitions
    # (they contain the bare function signature, not a call expression).
    case "$_line" in
      *"func "*"ClientForUser("*|*"func "*"RESTMapper()"*|*"func "*"DiscoveryClient()"*|*"// "*) continue ;;
    esac

    # Exempt when annotated on the previous line.
    nolint_on_prev_line "$_abs" "$_lineno" && continue

    printf 'VIOLATION  %s:%d\n  %s\n' "$_rel" "$_lineno" "$_line"
  done < "$_abs"
}

# -----------------------------------------------------------------------
# Main scan — collect all violations into a temp file to avoid subshell
# variable-scope issues with `find ... | while`.
# -----------------------------------------------------------------------

TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT INT TERM

printf '\n[check-cluster-routing] scanning handler directories for direct K8sClient calls...\n\n'

for _dir in $HANDLER_DIRS; do
  _abs_dir="$ROOT/$_dir"
  [ -d "$_abs_dir" ] || continue

  # Use a temp list file to iterate without a pipeline subshell.
  _listfile="$(mktemp)"
  find "$_abs_dir" -name '*.go' | sort > "$_listfile"

  while IFS= read -r _go_file; do
    scan_file "$_go_file" >> "$TMPFILE"
  done < "$_listfile"

  rm -f "$_listfile"
done

# -----------------------------------------------------------------------
# Report
# -----------------------------------------------------------------------

VIOLATIONS=0
if [ -s "$TMPFILE" ]; then
  cat "$TMPFILE"
  # Count VIOLATION lines (one per hit, always starts the pair).
  VIOLATIONS=$(grep -c '^VIOLATION' "$TMPFILE" || true)
fi

printf '\n[check-cluster-routing] scan complete.\n'

GATE="${CHECK_CLUSTER_ROUTING_GATE:-warn}"
printf '[check-cluster-routing] gate mode: %s\n' "$GATE"

if [ "$VIOLATIONS" -eq 0 ]; then
  printf 'OK — no unexempt direct K8sClient calls found.\n\n'
  exit 0
fi

printf 'FOUND %d violation(s) — handlers calling .ClientForUser / .DynamicClientForUser\n' "$VIOLATIONS"
printf '(or, in SCHEMA_ROUTED_DIRS, .RESTMapper / .DiscoveryClient)\n'
printf 'directly instead of routing through ClusterRouter.\n\n'
printf 'To fix: replace h.K8sClient.ClientForUser / DynamicClientForUser with\n'
printf '        h.ClusterRouter.ClientForCluster / DynamicClientForCluster, and\n'
printf '        .RESTMapper / .DiscoveryClient with the Mapper / Discovery of the\n'
printf '        TargetSchema returned by h.ClusterRouter.TargetFor / TargetSchemaFor.\n'
printf 'To suppress a legitimate call site, add a comment on the line above:\n'
printf '    // nolint:cluster-routing <reason>\n\n'

if [ "$GATE" = "warn" ]; then
  printf '[warn mode] CHECK_CLUSTER_ROUTING_GATE=warn — exiting 0 (bootstrap phase).\n'
  printf 'Flip to CHECK_CLUSTER_ROUTING_GATE=fail once Phase 2 rewrites land.\n\n'
  exit 0
fi

exit 1
