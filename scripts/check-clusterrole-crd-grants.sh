#!/usr/bin/env sh
# scripts/check-clusterrole-crd-grants.sh
#
# Guard against the #305 -> #326 regression class:
#
#   A CRD-discovered feature lists its resources cluster-wide as the SERVICE
#   ACCOUNT (k8sClient.BaseDynamicClient()), then filters per-user with a
#   SelfSubjectAccessReview. If the Helm ClusterRole is missing the matching
#   apiGroup grant, the SA List() returns Forbidden, the feature's cache fetch
#   500s, and the dashboard renders "Failed to load ... status" wherever those
#   CRDs are installed.
#
#   PR #305 dropped the ClusterRole [*] wildcard and re-added explicit grants
#   but forgot five groups (gateway / velero / aquasecurity / kubescape /
#   flux-notifications). This guard makes that omission a CI failure instead of
#   a production incident.
#
# How it works: cross-check every dotted CRD apiGroup referenced by a
# service-account-listing package in backend/internal against the apiGroups
# granted anywhere in the Helm ClusterRole (a single grant covers all SA List
# calls on that group — RBAC is additive across the role).
#
# A group is EXEMPT (ALLOWLIST below) when it is reached ONLY via the
# impersonated client — the API server RBACs those calls against the requesting
# user, so the service account needs no standing grant.
#
# Detection is intentionally scoped to BaseDynamicClient() — the canonical
# SA-list entry point and the exact mechanism the #305 regression broke.
# Informer-based SA listing (local-cluster real-time WebSocket feeds) is a
# small, stable set of already-granted groups managed separately.
#
# Usage:
#   bash scripts/check-clusterrole-crd-grants.sh          # fail mode (default)
#   CHECK_CRD_GRANTS_GATE=warn bash scripts/check-clusterrole-crd-grants.sh
#     -- warn-only; prints gaps but exits 0.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTERROLE="$ROOT/helm/kubecenter/templates/clusterrole.yaml"
SCAN_DIR="$ROOT/backend/internal"

# Groups referenced by an SA-listing package but reached ONLY via the
# impersonated client. Keep short; justify every entry.
#
#   acme.cert-manager.io — cert-manager Order/Challenge detail is fetched with
#     the impersonating client in HandleGetCertificate (handler.go), never the
#     service account. No standing grant required.
ALLOWLIST="acme.cert-manager.io"

is_allowlisted() {
  for _g in $ALLOWLIST; do
    [ "$1" = "$_g" ] && return 0
  done
  return 1
}

[ -f "$CLUSTERROLE" ] || { printf 'ERROR: %s not found\n' "$CLUSTERROLE" >&2; exit 2; }

# 1. apiGroups granted anywhere in the ClusterRole.
GRANTED="$(grep -oE 'apiGroups: \[[^]]*\]' "$CLUSTERROLE" \
  | grep -oE '"[^"]*"' | tr -d '"' | sort -u)"

# 2. Packages that list as the service account.
SA_PKGS="$(grep -rl 'BaseDynamicClient(' "$SCAN_DIR" --include='*.go' \
  | grep -v '_test\.go' | xargs -n1 dirname | sort -u)"

# 3. Dotted CRD apiGroups referenced by those packages (non-test files only).
#    The dot filter drops core/built-in groups ("", apps, batch, ...) which are
#    always granted, leaving CRD groups.
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT INT TERM
for _pkg in $SA_PKGS; do
  for _f in "$_pkg"/*.go; do
    case "$_f" in *_test.go) continue ;; esac
    [ -f "$_f" ] || continue
    grep -ohE '(Group|APIGroup):[[:space:]]*"[a-z0-9-]+(\.[a-z0-9-]+)+"' "$_f" 2>/dev/null || true
  done
done | grep -oE '"[^"]*"' | tr -d '"' | sort -u > "$TMP"

printf '\n[check-clusterrole-crd-grants] cross-checking SA-listed CRD groups vs ClusterRole grants...\n\n'

GAPS=0
while IFS= read -r _grp; do
  [ -n "$_grp" ] || continue
  if echo "$GRANTED" | grep -qx "$_grp"; then
    printf '  OK     %s\n' "$_grp"
  elif is_allowlisted "$_grp"; then
    printf '  EXEMPT %s (impersonation-only)\n' "$_grp"
  else
    printf '  GAP    %s — listed by the service account but NOT granted in the ClusterRole\n' "$_grp"
    GAPS=$(( GAPS + 1 ))
  fi
done < "$TMP"

printf '\n[check-clusterrole-crd-grants] scan complete.\n'
GATE="${CHECK_CRD_GRANTS_GATE:-fail}"
printf '[check-clusterrole-crd-grants] gate mode: %s\n' "$GATE"

if [ "$GAPS" -eq 0 ]; then
  printf 'OK — every service-account-listed CRD group has a matching ClusterRole grant.\n\n'
  exit 0
fi

printf '\nFOUND %d ungranted CRD group(s) listed by the service account.\n' "$GAPS"
printf 'Each gap means the feature dashboard will 500 wherever those CRDs are installed.\n\n'
printf 'To fix: add the group to helm/kubecenter/templates/clusterrole.yaml\n'
printf '        (feature list/watch block + Extensions Hub count block).\n'
printf 'If the group is genuinely impersonation-only, add it to ALLOWLIST in this\n'
printf 'script with a one-line justification.\n\n'

if [ "$GATE" = "warn" ]; then
  printf '[warn mode] CHECK_CRD_GRANTS_GATE=warn — exiting 0.\n\n'
  exit 0
fi
exit 1
