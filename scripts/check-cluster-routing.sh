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
#   - The line itself is a func declaration for ClientForUser/RESTMapper/
#     DiscoveryClient, or a whole-line comment (its first non-whitespace
#     characters are `//`). A trailing inline comment on a real call
#     (`h.K8sClient.RESTMapper() // keep`) does NOT exempt the line — see
#     classify_line below.
#
# Self-test: before every scan, run_self_test exercises classify_line
# directly against a fixed set of known-good/known-bad cases (the same
# function scan_file uses on real files) and exits non-zero if any case
# doesn't match, REGARDLESS of CHECK_CLUSTER_ROUTING_GATE. This catches
# matcher regressions even though CI only runs the scan itself in warn mode.
#
# Usage:
#   bash scripts/check-cluster-routing.sh
#     -- warn mode when CHECK_CLUSTER_ROUTING_GATE=warn (default bootstrap);
#        prints all violations but exits 0.
#   CHECK_CLUSTER_ROUTING_GATE=fail bash scripts/check-cluster-routing.sh
#     -- strict mode; exits non-zero when any unexempt violation is found.
#
# Exits non-zero (strict mode only) for real violations; always prints every
# violation before exiting so CI surfaces all sites in one run. Exits
# non-zero in ANY gate mode if the self-test fails.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# A literal tab character, computed once, for classify_line's whole-line
# comment check and for building self-test fixtures.
TAB="$(printf '\t')"

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

# is_whole_line_comment LINE — returns 0 (true) if the first non-whitespace
# characters of LINE are `//` (a whole-line comment). A line with a real
# call and a trailing inline comment (`foo() // keep`) is NOT a whole-line
# comment and returns 1 (Finding #2 — the old `*"// "*` check matched
# ANY line containing "// " anywhere, which silently exempted real calls
# with trailing comments even in fail mode).
is_whole_line_comment() {
  _l="$1"
  while :; do
    case "$_l" in
      " "*|"$TAB"*) _l="${_l#?}" ;;
      *) break ;;
    esac
  done
  case "$_l" in
    "//"*) return 0 ;;
  esac
  return 1
}

# classify_line REL_PATH LINE PREV_LINE — returns 0 (true) if LINE at
# REL_PATH is an unexempt cluster-routing violation, given PREV_LINE (the
# source line immediately above, used for the nolint check, or "" if LINE
# is the first line of the file). Returns 1 otherwise.
#
# Pure function — no file I/O — so run_self_test can exercise the exact
# same logic scan_file uses on real files without needing fixture files on
# disk.
classify_line() {
  _path="$1"
  _line="$2"
  _prev="$3"

  is_allowed_path "$_path" && return 1

  _schema=0
  is_schema_routed "$_path" && _schema=1

  _hit=0
  case "$_line" in
    *".ClientForUser("*|*".DynamicClientForUser("*) _hit=1 ;;
    *".RESTMapper()"*|*".DiscoveryClient()"*)
      if [ "$_schema" -eq 1 ]; then _hit=1; fi ;;
  esac
  [ "$_hit" -eq 1 ] || return 1

  # Skip lines that are interface / type / comment definitions
  # (they contain the bare function signature, not a call expression).
  case "$_line" in
    *"func "*"ClientForUser("*|*"func "*"RESTMapper()"*|*"func "*"DiscoveryClient()"*) return 1 ;;
  esac

  # Exempt whole-line comments only — a trailing inline comment on a real
  # call is still a violation (Finding #2).
  is_whole_line_comment "$_line" && return 1

  # Exempt when annotated on the previous line.
  case "$_prev" in
    *"// nolint:cluster-routing "*) return 1 ;;
  esac

  return 0
}

# scan_file FILE — writes violation lines to stdout.
# Each violation is two lines: "VIOLATION rel/path:N" then "  <source line>".
scan_file() {
  _abs="$1"
  _rel="${_abs#"$ROOT/"}"

  _lineno=0
  _prevline=""
  while IFS= read -r _line; do
    _lineno=$(( _lineno + 1 ))
    if classify_line "$_rel" "$_line" "$_prevline"; then
      printf 'VIOLATION  %s:%d\n  %s\n' "$_rel" "$_lineno" "$_line"
    fi
    _prevline="$_line"
  done < "$_abs"
}

# -----------------------------------------------------------------------
# Self-test (Finding #3) — runs automatically before every scan, in every
# gate mode. Exercises classify_line directly against known-good/known-bad
# cases so a detector regression fails the run even though CI only invokes
# the scan itself in warn mode.
# -----------------------------------------------------------------------

run_self_test() {
  _n=0
  _pass=0

  # 1: RESTMapper call in a schema-routed dir -> violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" ""; then
    _pass=$(( _pass + 1 ))
  else
    printf '[check-cluster-routing] self-test FAILED: case %d — RESTMapper() in a schema-routed dir must be a violation\n' "$_n" >&2
    exit 1
  fi

  # 2: same call, non-schema-routed dir -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/certmanager/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" ""; then
    printf '[check-cluster-routing] self-test FAILED: case %d — RESTMapper() outside SCHEMA_ROUTED_DIRS must NOT be a violation\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  # 3: ClientForUser call, non-schema-routed dir -> violation (unconditional pattern)
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/certmanager/x.go" "${TAB}cs, err := h.K8sClient.ClientForUser(u, g)" ""; then
    _pass=$(( _pass + 1 ))
  else
    printf '[check-cluster-routing] self-test FAILED: case %d — ClientForUser() must be a violation regardless of schema routing\n' "$_n" >&2
    exit 1
  fi

  # 4 (Finding #2): RESTMapper call with a trailing inline comment -> still a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper() // keep" ""; then
    _pass=$(( _pass + 1 ))
  else
    printf '[check-cluster-routing] self-test FAILED: case %d — a trailing inline comment must NOT exempt a real call (Finding #2)\n' "$_n" >&2
    exit 1
  fi

  # 5: whole-line comment merely mentioning the call -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yaml/x.go" "${TAB}// h.K8sClient.RESTMapper() is not used here" ""; then
    printf '[check-cluster-routing] self-test FAILED: case %d — a whole-line comment must NOT be a violation\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  # 6: func declaration -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yaml/x.go" "func (f *ClientFactory) RESTMapper() meta.RESTMapper {" ""; then
    printf '[check-cluster-routing] self-test FAILED: case %d — a func declaration must NOT be a violation\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  # 7: nolint on previous line -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" "${TAB}// nolint:cluster-routing reason here"; then
    printf '[check-cluster-routing] self-test FAILED: case %d — // nolint:cluster-routing on the previous line must exempt\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  # 8: DiscoveryClient call in server (schema-routed) -> violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/server/x.go" "${TAB}disc := h.K8sClient.DiscoveryClient()" ""; then
    _pass=$(( _pass + 1 ))
  else
    printf '[check-cluster-routing] self-test FAILED: case %d — DiscoveryClient() in the schema-routed server dir must be a violation\n' "$_n" >&2
    exit 1
  fi

  # 9: same call, sibling dir with an overlapping name prefix -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/yamlextra/x.go" "${TAB}disc := h.K8sClient.DiscoveryClient()" ""; then
    printf '[check-cluster-routing] self-test FAILED: case %d — yamlextra must not match the yaml schema-routed dir prefix\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  # 10: ClientForUser call under an ALLOWED_PREFIXES path -> not a violation
  _n=$(( _n + 1 ))
  if classify_line "backend/internal/k8s/client.go" "${TAB}cs, err := h.K8sClient.ClientForUser(u, g)" ""; then
    printf '[check-cluster-routing] self-test FAILED: case %d — an ALLOWED_PREFIXES path must NOT be a violation\n' "$_n" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi

  printf '[check-cluster-routing] self-test: %d/%d detector cases passed\n' "$_pass" "$_n"
}

run_self_test

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
