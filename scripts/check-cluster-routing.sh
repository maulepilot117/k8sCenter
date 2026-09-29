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
# R-8 (U12) — in the packages listed in REMOTE_ROUTED_DIRS, also flag
# .BaseDynamicClient() / .BaseClientset() / .DiscoveryClient() /
# .RESTMapper() and informer-cache reads (.Informers.). Those read the LOCAL
# cluster through the service account, the local schema or local informers; a feature package that has migrated to serve the
# selected cluster must mark every remaining local read with a nolint
# reason, so serving local data under a remote cluster's name is visible.
#
# A line is exempt when:
#   - The line above carries `// nolint:cluster-routing` AND a free-form reason
#   - The file path matches one of the ALLOWED_PREFIXES below (informer
#     setup, prober, the LocalFactory() implementation in cluster_router.go,
#     client.go where the methods are defined)
#   - The line itself is a whole-line comment (its first non-whitespace
#     characters are `//`). A trailing inline comment on a real call
#     (`h.K8sClient.RESTMapper() // keep`) does NOT exempt the line — see
#     classify_line below.
#
# There is deliberately no separate func-declaration exemption: a genuine
# declaration of ClientForUser/RESTMapper/DiscoveryClient (a method
# definition or an interface method line) never contains the dot-prefixed
# call form the detector matches on (`.RESTMapper()`, `.DiscoveryClient()`,
# `.ClientForUser(`, `.DynamicClientForUser(`), so it is already clean
# without any extra rule. A dedicated exemption used to live here and also
# matched a gofmt-legal one-line method body or an inline func literal that
# makes a real dot-prefixed call on the same line as a `func ` token —
# hiding real violations (Finding #3).
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

# Directories (relative to ROOT) whose feature handlers serve the cluster
# the request selects (R-8). In these, a service-account, local-schema or
# informer read (.BaseDynamicClient() / .BaseClientset() / .DiscoveryClient()
# / .RESTMapper() / .Informers.) is a violation unless annotated: each remaining local
# read must say why it is local. A package joins this list in the unit that
# migrates it.
REMOTE_ROUTED_DIRS="backend/internal/gateway"

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

# is_remote_routed PATH — returns 0 (true) if PATH is inside a
# REMOTE_ROUTED_DIRS entry.
is_remote_routed() {
  _p="$1"
  for _dir in $REMOTE_ROUTED_DIRS; do
    case "$_p" in
      "$_dir"/*) return 0 ;;
    esac
  done
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
  _remote=0
  is_remote_routed "$_path" && _remote=1

  _hit=0
  case "$_line" in
    *".ClientForUser("*|*".DynamicClientForUser("*) _hit=1 ;;
    *".RESTMapper()"*|*".DiscoveryClient()"*)
      if [ "$_schema" -eq 1 ] || [ "$_remote" -eq 1 ]; then _hit=1; fi ;;
    *".BaseDynamicClient()"*|*".BaseClientset()"*|*".Informers."*)
      if [ "$_remote" -eq 1 ]; then _hit=1; fi ;;
  esac
  [ "$_hit" -eq 1 ] || return 1

  # No func-declaration exemption: see the header comment.

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

# expect_violation LABEL PATH LINE PREV — asserts classify_line treats LINE
# (at PATH, with previous source line PREV) as a violation. Shares the _n/
# _pass counters with expect_clean; prints a labeled failure to stderr and
# exits 1 immediately on a mismatch, regardless of CHECK_CLUSTER_ROUTING_GATE.
expect_violation() {
  _label="$1"
  _n=$(( _n + 1 ))
  if classify_line "$2" "$3" "$4"; then
    _pass=$(( _pass + 1 ))
  else
    printf '[check-cluster-routing] self-test FAILED: case %d — %s\n' "$_n" "$_label" >&2
    exit 1
  fi
}

# expect_clean LABEL PATH LINE PREV — asserts classify_line treats LINE as
# NOT a violation. Same counters and fail-and-exit behavior as
# expect_violation, inverted.
expect_clean() {
  _label="$1"
  _n=$(( _n + 1 ))
  if classify_line "$2" "$3" "$4"; then
    printf '[check-cluster-routing] self-test FAILED: case %d — %s\n' "$_n" "$_label" >&2
    exit 1
  else
    _pass=$(( _pass + 1 ))
  fi
}

run_self_test() {
  _n=0
  _pass=0

  expect_violation "RESTMapper() in a schema-routed dir must be a violation" \
    "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" ""

  expect_clean "RESTMapper() outside SCHEMA_ROUTED_DIRS must NOT be a violation" \
    "backend/internal/certmanager/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" ""

  expect_violation "ClientForUser() must be a violation regardless of schema routing" \
    "backend/internal/certmanager/x.go" "${TAB}cs, err := h.K8sClient.ClientForUser(u, g)" ""

  # Finding #2
  expect_violation "a trailing inline comment must NOT exempt a real call (Finding #2)" \
    "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper() // keep" ""

  expect_clean "a whole-line comment must NOT be a violation" \
    "backend/internal/yaml/x.go" "${TAB}// h.K8sClient.RESTMapper() is not used here" ""

  expect_clean "a func declaration must NOT be a violation" \
    "backend/internal/yaml/x.go" "func (f *ClientFactory) RESTMapper() meta.RESTMapper {" ""

  expect_clean "// nolint:cluster-routing on the previous line must exempt" \
    "backend/internal/yaml/x.go" "${TAB}mapper := h.K8sClient.RESTMapper()" "${TAB}// nolint:cluster-routing reason here"

  expect_violation "DiscoveryClient() in the schema-routed server dir must be a violation" \
    "backend/internal/server/x.go" "${TAB}disc := h.K8sClient.DiscoveryClient()" ""

  expect_clean "yamlextra must not match the yaml schema-routed dir prefix" \
    "backend/internal/yamlextra/x.go" "${TAB}disc := h.K8sClient.DiscoveryClient()" ""

  expect_clean "an ALLOWED_PREFIXES path must NOT be a violation" \
    "backend/internal/k8s/client.go" "${TAB}cs, err := h.K8sClient.ClientForUser(u, g)" ""

  # Finding #3 — a gofmt-legal one-line method body whose BODY makes a real
  # call must still be a violation; the old func-declaration exemption
  # matched "func " anywhere on the line followed by "RESTMapper()" anywhere
  # after, so it wrongly exempted this.
  expect_violation "a one-line method body making a real dot-prefixed call must be a violation (Finding #3)" \
    "backend/internal/yaml/x.go" "${TAB}func (h *Handler) m() meta.RESTMapper { return h.K8sClient.RESTMapper() }" ""

  # Finding #3 — a real call inside an inline func literal on the same line
  # must still be a violation.
  expect_violation "a call inside an inline func literal must be a violation (Finding #3)" \
    "backend/internal/yaml/x.go" "${TAB}defer func() { _ = h.K8sClient.RESTMapper() }()" ""

  # Finding #3 — a genuine method declaration in a schema-routed dir stays
  # clean without any dedicated func-declaration exemption, because it never
  # contains the dot-prefixed call form.
  expect_clean "a genuine method declaration in a schema-routed dir must NOT be a violation (Finding #3)" \
    "backend/internal/server/x.go" "func (f *ClientFactory) RESTMapper() meta.RESTMapper {" ""

  # Finding #3 — an interface method line in a schema-routed dir stays clean
  # for the same reason.
  expect_clean "an interface method line in a schema-routed dir must NOT be a violation (Finding #3)" \
    "backend/internal/server/x.go" "${TAB}RESTMapper() meta.RESTMapper" ""

  # R-8 (U12) — REMOTE_ROUTED_DIRS. The real list starts empty and grows as
  # packages migrate, so these cases run against a fixture list and restore
  # the real one afterwards.
  _saved_remote_routed="$REMOTE_ROUTED_DIRS"
  REMOTE_ROUTED_DIRS="backend/internal/gitops"

  expect_violation "BaseDynamicClient() in a remote-routed dir must be a violation" \
    "backend/internal/gitops/x.go" "${TAB}dyn := h.K8sClient.BaseDynamicClient()" ""

  expect_violation "BaseClientset() in a remote-routed dir must be a violation" \
    "backend/internal/gitops/x.go" "${TAB}cs := d.k8sClient.BaseClientset()" ""

  expect_violation "DiscoveryClient() in a remote-routed dir must be a violation" \
    "backend/internal/gitops/discovery.go" "${TAB}disc := d.k8sClient.DiscoveryClient()" ""

  expect_violation "RESTMapper() in a remote-routed dir must be a violation" \
    "backend/internal/gitops/x.go" "${TAB}m := h.K8sClient.RESTMapper()" ""

  expect_clean "BaseDynamicClient() outside REMOTE_ROUTED_DIRS must NOT be a violation" \
    "backend/internal/velero/x.go" "${TAB}dyn := h.K8sClient.BaseDynamicClient()" ""

  expect_clean "an annotated local read in a remote-routed dir must NOT be a violation" \
    "backend/internal/gitops/x.go" "${TAB}dyn := h.K8sClient.BaseDynamicClient()" "${TAB}// nolint:cluster-routing local path: local service-account cache"

  expect_clean "gitopsextra must not match the gitops remote-routed dir prefix" \
    "backend/internal/gitopsextra/x.go" "${TAB}dyn := h.K8sClient.BaseDynamicClient()" ""

  REMOTE_ROUTED_DIRS="backend/internal/storage"
  expect_violation "an informer-cache read in a remote-routed dir must be a violation" \
    "backend/internal/storage/x.go" "${TAB}drivers, err := h.Informers.CSIDrivers().List(labels.Everything())" ""

  expect_clean "an informer-cache read outside REMOTE_ROUTED_DIRS must NOT be a violation" \
    "backend/internal/velero/x.go" "${TAB}pods, err := h.Informers.Pods().List(sel)" ""

  REMOTE_ROUTED_DIRS="$_saved_remote_routed"

  printf '[check-cluster-routing] self-test: %d/%d detector cases passed\n' "$_pass" "$_n"
}

run_self_test

# check_routed_dirs — every SCHEMA_ROUTED_DIRS / REMOTE_ROUTED_DIRS entry
# must be an existing directory with no trailing slash that sits at or under
# a HANDLER_DIRS entry. A typo'd entry would otherwise match no file and
# silently guard nothing. Exits 1 in every gate mode.
check_routed_dirs() {
  for _entry in $SCHEMA_ROUTED_DIRS $REMOTE_ROUTED_DIRS; do
    case "$_entry" in
      */)
        printf '[check-cluster-routing] routed-dir entry %s must not end in /\n' "$_entry" >&2
        exit 1 ;;
    esac
    if [ ! -d "$ROOT/$_entry" ]; then
      printf '[check-cluster-routing] routed-dir entry %s is not a directory\n' "$_entry" >&2
      exit 1
    fi
    _scanned=0
    for _h in $HANDLER_DIRS; do
      case "$_entry" in
        "$_h"|"$_h"/*) _scanned=1 ;;
      esac
    done
    if [ "$_scanned" -eq 0 ]; then
      printf '[check-cluster-routing] routed-dir entry %s is not under any HANDLER_DIRS entry, so it is never scanned\n' "$_entry" >&2
      exit 1
    fi
  done
}

check_routed_dirs

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
printf '(or, in SCHEMA_ROUTED_DIRS, .RESTMapper / .DiscoveryClient;\n'
printf ' or, in REMOTE_ROUTED_DIRS, any local service-account or schema read)\n'
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
