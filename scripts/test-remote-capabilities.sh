#!/usr/bin/env sh
# scripts/test-remote-capabilities.sh
#
# Release C (U12) — build the two-cluster fixture that
# e2e/tests/remote-capabilities.spec.ts runs against, and tear it down again.
#
# What it creates on the REMOTE cluster, every object labelled
# app.kubernetes.io/managed-by=k8scenter-e2e-remote-fixture:
#   - a remote-only CRD, widgets.k8scenter.test, that the local cluster does
#     NOT have (the object AE2 previews and applies);
#   - namespace k8scenter-remote-fixture;
#   - ServiceAccount k8scenter-remote-probe with `impersonate` on users and on
#     a NAMED set of groups. k8sCenter's connection test
#     (ProbeImpersonateRights) asks about users with no resourceName, so users
#     stay unrestricted. Groups are limited to exactly the ones k8sCenter sends
#     for the admin (kubernetesGroups from /auth/me) plus system:authenticated,
#     so the 24h token cannot impersonate system:masters;
#   - a deliberately NARROWER role for the impersonated k8sCenter identity:
#     cluster-wide read on nodes, pods and services (the dashboard summary),
#     and read/write on widgets only inside the fixture namespace. No secrets,
#     no cluster-wide writes. That is the "differing identities" axis.
#
# It then registers the cluster through POST /api/v1/clusters with the real
# CA bundle — allowInsecureTLS stays false, so the production TLS path is the
# one exercised — and prints K8SCENTER_REMOTE_CLUSTER_ID=<id>.
#
# THE REMOTE API SERVER MUST BE REACHABLE AT A PUBLIC ADDRESS.
# k8sCenter's SSRF guard refuses loopback, RFC1918, link-local, CGNAT and
# unspecified addresses at registration and on every dial. A kind cluster on a
# laptop (127.0.0.1) or a homelab (10.x, 192.168.x) is refused, and that is
# correct behaviour, not something this script works around. Run it where the
# remote cluster has a public name: a cloud VM running kind, or an existing
# public cluster via KUBECENTER_REMOTE_CONTEXT.
#
# Usage:
#   sh scripts/test-remote-capabilities.sh              # create + register
#   sh scripts/test-remote-capabilities.sh --teardown   # remove fixture objects
#
# Environment:
#   KUBECENTER_URL               k8sCenter backend base URL
#                                (default http://localhost:8080)
#   KUBECENTER_ADMIN_USER        admin account to register with (required;
#   KUBECENTER_ADMIN_PASSWORD    non-local clusters are admin-only)
#   KUBECENTER_REMOTE_CONTEXT    use this existing kube context as the remote
#                                instead of creating a kind cluster
#   KUBECENTER_REMOTE_API_HOST   public DNS name or IP of the kind host
#                                (required when creating the kind cluster)
#   KUBECENTER_REMOTE_API_URL    API server URL to register (default: from
#                                the kube context; https://HOST:16443 for kind)
#   KUBECENTER_REMOTE_CLUSTER_ID with --teardown, also deregister this id. Pass
#                                the CURRENT registration: if the cluster was
#                                deleted and re-registered by hand, that is
#                                the new id printed then, not the original.
#   KUBECENTER_REMOTE_REGISTRATION_FILE
#                                if set, also write the registration request
#                                body there (always mode 600, replacing any
#                                existing file). It holds the probe token, so
#                                treat it as a credential. The e2e spec's
#                                delete-and-re-register case replays it and
#                                skips when it is absent.
#
# Teardown needs the same environment as create (KUBECENTER_URL, the admin
# credentials when deregistering, and KUBECENTER_REMOTE_CONTEXT if create used
# one). It deletes ONLY objects carrying the fixture label, selected by label,
# never by name. Before applying, create refuses to touch any fixed-name object
# that already exists WITHOUT the fixture label, so the label only ever marks
# objects this fixture created and teardown cannot remove anything else.
#
# The kind cluster is deleted only when it carries the ownership marker —
# ConfigMap kube-system/k8scenter-remote-fixture-owner, fixture-labelled — that
# this script writes right after `kind create cluster`. A pre-existing kind
# cluster named k8scenter-remote without the marker is never adopted by create
# and never deleted by teardown.
#
# Requires: kubectl, curl (7.55+), jq; kind unless KUBECENTER_REMOTE_CONTEXT
# is set.

set -eu
# Every file this script writes (temp secrets, the registration body) is
# owner-only.
umask 077

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

FIXTURE_LABEL_KEY="app.kubernetes.io/managed-by"
FIXTURE_LABEL_VALUE="k8scenter-e2e-remote-fixture"
FIXTURE_SELECTOR="${FIXTURE_LABEL_KEY}=${FIXTURE_LABEL_VALUE}"
FIXTURE_NS="k8scenter-remote-fixture"
PROBE_SA="k8scenter-remote-probe"
KIND_NAME="k8scenter-remote"
OWNER_MARKER="k8scenter-remote-fixture-owner"
# kubectl jsonpath for the fixture label's value. Must name FIXTURE_LABEL_KEY
# with its dots escaped.
LABEL_JSONPATH='{.metadata.labels.app\.kubernetes\.io/managed-by}'
KIND_PORT="16443"
CLUSTER_NAME="e2e-remote"

KUBECENTER_URL="${KUBECENTER_URL:-http://localhost:8080}"
REMOTE_CONTEXT="${KUBECENTER_REMOTE_CONTEXT:-}"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT INT TERM

log() { printf '%s\n' "test-remote-capabilities: $*" >&2; }
die() { log "error: $*"; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not on PATH"
}

# kubectl against the remote cluster only. Every mutation in this script goes
# through here, so nothing can land on whatever the ambient context is.
rk() { kubectl --context "$CONTEXT" "$@"; }

resolve_context() {
  if [ -n "$REMOTE_CONTEXT" ]; then
    CONTEXT="$REMOTE_CONTEXT"
  else
    CONTEXT="kind-${KIND_NAME}"
  fi
}

# fixture_label_of ARGS... — prints the fixture label's value on the object
# named by ARGS ("" if it has none), prints NOTFOUND if the object does not
# exist, and fails on any other error (unreachable cluster, forbidden, ...).
fixture_label_of() {
  exists="$(rk get "$@" --ignore-not-found -o name)" || return 1
  if [ -z "$exists" ]; then
    printf 'NOTFOUND'
    return 0
  fi
  rk get "$@" -o "jsonpath=${LABEL_JSONPATH}"
}

# owns_kind_cluster — true only when the kind cluster carries the ownership
# marker this script writes right after creating it.
owns_kind_cluster() {
  v="$(fixture_label_of -n kube-system "configmap/${OWNER_MARKER}" 2>/dev/null)" ||
    return 1
  [ "$v" = "$FIXTURE_LABEL_VALUE" ]
}

# ---------------------------------------------------------------- teardown --

teardown() {
  need kubectl
  resolve_context

  if [ -n "${KUBECENTER_REMOTE_CLUSTER_ID:-}" ]; then
    login
    log "deregistering cluster ${KUBECENTER_REMOTE_CLUSTER_ID} from k8sCenter"
    api DELETE "/api/v1/clusters/${KUBECENTER_REMOTE_CLUSTER_ID}" >/dev/null ||
      log "deregistration failed; continuing with object cleanup"
  fi

  if kubectl config get-contexts "$CONTEXT" >/dev/null 2>&1; then
    log "deleting fixture-labelled objects on ${CONTEXT} (selector ${FIXTURE_SELECTOR})"
    # Namespaced objects first, then the namespace, then cluster-scoped ones.
    # Every call selects by label only.
    rk delete widgets.k8scenter.test --all-namespaces -l "$FIXTURE_SELECTOR" \
      --ignore-not-found 2>/dev/null || true
    rk delete rolebindings,roles,serviceaccounts --all-namespaces \
      -l "$FIXTURE_SELECTOR" --ignore-not-found
    rk delete namespaces -l "$FIXTURE_SELECTOR" --ignore-not-found
    rk delete clusterrolebindings,clusterroles,customresourcedefinitions \
      -l "$FIXTURE_SELECTOR" --ignore-not-found
    left="$(rk get clusterroles,clusterrolebindings,customresourcedefinitions,namespaces \
      -l "$FIXTURE_SELECTOR" -o name 2>/dev/null || true)"
    [ -z "$left" ] || die "fixture objects survived teardown: ${left}"
  else
    log "context ${CONTEXT} not found; nothing to delete there"
  fi

  if [ -z "$REMOTE_CONTEXT" ]; then
    need kind
    if kind get clusters 2>/dev/null | grep -qx "$KIND_NAME"; then
      if owns_kind_cluster; then
        log "deleting kind cluster ${KIND_NAME} (ownership marker kube-system/${OWNER_MARKER} present)"
        kind delete cluster --name "$KIND_NAME"
      else
        log "leaving kind cluster ${KIND_NAME} in place: it has no ownership marker kube-system/${OWNER_MARKER} (or is unreachable), so this script did not create it"
      fi
    fi
  fi
  log "teardown complete"
}

# ------------------------------------------------------------- k8sCenter API --

ACCESS_TOKEN=""
AUTH_HEADER_FILE=""

login() {
  need curl
  need jq
  [ -n "${KUBECENTER_ADMIN_USER:-}" ] || die "KUBECENTER_ADMIN_USER is required"
  [ -n "${KUBECENTER_ADMIN_PASSWORD:-}" ] || die "KUBECENTER_ADMIN_PASSWORD is required"
  # Secrets never go on a command line (visible in the process list): printf
  # is a shell builtin, and jq and curl read them from owner-only files.
  pw_file="$TMP_DIR/admin-password"
  printf '%s' "$KUBECENTER_ADMIN_PASSWORD" >"$pw_file"
  body_file="$TMP_DIR/login.json"
  jq -n --arg u "$KUBECENTER_ADMIN_USER" --rawfile p "$pw_file" \
    '{username: $u, password: ($p | rtrimstr("\n"))}' >"$body_file"
  rm -f "$pw_file"
  # Login shares the 5 req/min per-IP auth bucket, so this runs once.
  resp="$(curl -sS -X POST "${KUBECENTER_URL}/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
    --data-binary "@${body_file}")" || die "login request failed"
  rm -f "$body_file"
  ACCESS_TOKEN="$(printf '%s' "$resp" | jq -r '.data.accessToken // empty')"
  [ -n "$ACCESS_TOKEN" ] || die "login failed: $(printf '%s' "$resp" | jq -c '.error // .')"
  # curl reads the bearer header from this file (-H @file) so the access
  # token stays out of argv too.
  AUTH_HEADER_FILE="$TMP_DIR/auth-header"
  printf 'Authorization: Bearer %s\n' "$ACCESS_TOKEN" >"$AUTH_HEADER_FILE"
}

# api METHOD PATH [JSON-FILE] — prints the body, fails on a non-2xx status.
api() {
  out="$TMP_DIR/api-response"
  if [ $# -ge 3 ]; then
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$1" "${KUBECENTER_URL}$2" \
      -H "@${AUTH_HEADER_FILE}" \
      -H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
      --data-binary "@$3")"
  else
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$1" "${KUBECENTER_URL}$2" \
      -H "@${AUTH_HEADER_FILE}" \
      -H 'X-Requested-With: XMLHttpRequest')"
  fi
  case "$status" in
    2*) cat "$out" ;;
    *) log "$1 $2 -> HTTP ${status}: $(cat "$out")"; return 1 ;;
  esac
}

# ------------------------------------------------------------------ create --

create_kind_cluster() {
  need kind
  [ -n "${KUBECENTER_REMOTE_API_HOST:-}" ] ||
    die "KUBECENTER_REMOTE_API_HOST (the kind host's public name or IP) is required to create the kind cluster"
  if kind get clusters 2>/dev/null | grep -qx "$KIND_NAME"; then
    owns_kind_cluster ||
      die "a kind cluster named ${KIND_NAME} already exists without this script's ownership marker (kube-system/${OWNER_MARKER}); refusing to adopt a cluster it did not create, because teardown would then delete it. Remove or rename that cluster, or point KUBECENTER_REMOTE_CONTEXT at it explicitly."
    log "kind cluster ${KIND_NAME} already exists and carries the ownership marker; reusing it"
    return
  fi
  cfg="$TMP_DIR/remote-kind-config.yaml"
  # Substitute only the one placeholder; the rest of the file is literal.
  sed "s|\${KUBECENTER_REMOTE_API_HOST}|${KUBECENTER_REMOTE_API_HOST}|" \
    "$ROOT/e2e/remote-kind-config.yaml" >"$cfg"
  log "creating kind cluster ${KIND_NAME}"
  kind create cluster --config "$cfg" --wait 120s
  # Ownership proof lives IN the cluster, so a later create or teardown can
  # tell this cluster from a same-named one somebody else made.
  rk apply -f - <<EOF ||
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${OWNER_MARKER}
  namespace: kube-system
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
data:
  createdBy: scripts/test-remote-capabilities.sh
EOF
    die "created kind cluster ${KIND_NAME} but could not write its ownership marker; teardown will not delete it. Delete it with: kind delete cluster --name ${KIND_NAME}"
}

# Refuses to apply over any fixed-name object that exists without the fixture
# label. kubectl apply would otherwise adopt it and stamp the label on it, and
# the label-selected teardown would then delete it — for the namespace, with
# everything inside it. Only objects this fixture created may be re-applied.
preflight_fixture_names() {
  conflicts=""
  while read -r ns ref; do
    [ -n "$ref" ] || continue
    if [ "$ns" = "-" ]; then
      v="$(fixture_label_of "$ref")" || die "could not check ${ref} on ${CONTEXT}"
    else
      v="$(fixture_label_of -n "$ns" "$ref")" || die "could not check ${ns}/${ref} on ${CONTEXT}"
    fi
    case "$v" in
      NOTFOUND | "$FIXTURE_LABEL_VALUE") ;;
      *) conflicts="${conflicts} ${ns}/${ref}" ;;
    esac
  done <<EOF
- customresourcedefinition/widgets.k8scenter.test
- namespace/${FIXTURE_NS}
${FIXTURE_NS} serviceaccount/${PROBE_SA}
- clusterrole/k8scenter-remote-fixture-impersonator
- clusterrolebinding/k8scenter-remote-fixture-impersonator
- clusterrole/k8scenter-remote-fixture-reader
- clusterrolebinding/k8scenter-remote-fixture-reader
${FIXTURE_NS} role/k8scenter-remote-fixture-widgets
${FIXTURE_NS} rolebinding/k8scenter-remote-fixture-widgets
EOF
  [ -z "$conflicts" ] ||
    die "these objects already exist on ${CONTEXT} without the ${FIXTURE_SELECTOR} label, so this fixture did not create them:${conflicts} (- = cluster-scoped). Refusing to adopt them, because teardown would then delete them. Remove or rename them, or use a different cluster."
}

apply_fixture_objects() {
  user="$1"
  groups_json="$2"
  preflight_fixture_names
  log "applying fixture objects to ${CONTEXT} for impersonated user '${user}', groups ${groups_json}"
  # groups_json is a JSON array of strings, which is also a valid YAML flow
  # sequence. It is never empty (system:authenticated is always in it): an
  # empty resourceNames would grant impersonation of EVERY group.
  rk apply -f - <<EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.k8scenter.test
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
spec:
  group: k8scenter.test
  scope: Namespaced
  names:
    plural: widgets
    singular: widget
    kind: Widget
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                size:
                  type: string
---
apiVersion: v1
kind: Namespace
metadata:
  name: ${FIXTURE_NS}
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${PROBE_SA}
  namespace: ${FIXTURE_NS}
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8scenter-remote-fixture-impersonator
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
rules:
  # Unrestricted: ProbeImpersonateRights asks about users with no resourceName.
  - apiGroups: [""]
    resources: ["users"]
    verbs: ["impersonate"]
  # Only the groups k8sCenter sends for the admin, plus system:authenticated.
  - apiGroups: [""]
    resources: ["groups"]
    verbs: ["impersonate"]
    resourceNames: ${groups_json}
  # The connection test counts nodes with the ServiceAccount's own identity.
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8scenter-remote-fixture-impersonator
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: k8scenter-remote-fixture-impersonator
subjects:
  - kind: ServiceAccount
    name: ${PROBE_SA}
    namespace: ${FIXTURE_NS}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8scenter-remote-fixture-reader
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods", "services"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8scenter-remote-fixture-reader
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: k8scenter-remote-fixture-reader
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: User
    name: ${user}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: k8scenter-remote-fixture-widgets
  namespace: ${FIXTURE_NS}
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
rules:
  - apiGroups: ["k8scenter.test"]
    resources: ["widgets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: k8scenter-remote-fixture-widgets
  namespace: ${FIXTURE_NS}
  labels:
    ${FIXTURE_LABEL_KEY}: ${FIXTURE_LABEL_VALUE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: k8scenter-remote-fixture-widgets
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: User
    name: ${user}
EOF
  rk wait --for=condition=Established crd/widgets.k8scenter.test --timeout=60s
}

# Refuses early, with the reason, what k8sCenter's SSRF guard would refuse
# late with a generic error. A mirror of the backend check, not a replacement:
# the backend re-resolves on every dial regardless.
check_public_host() {
  host="$1"
  case "$host" in
    localhost | 127.* | 10.* | 192.168.* | 169.254.* | 0.* | "[::1]" | ::1)
      die "API server host '${host}' is loopback/private; k8sCenter will refuse it (SSRF guard). Use a public address." ;;
    172.1[6-9].* | 172.2[0-9].* | 172.3[01].*)
      die "API server host '${host}' is RFC1918; k8sCenter will refuse it (SSRF guard). Use a public address." ;;
    100.6[4-9].* | 100.[7-9][0-9].* | 100.1[01][0-9].* | 100.12[0-7].*)
      die "API server host '${host}' is CGNAT space; k8sCenter will refuse it (SSRF guard). Use a public address." ;;
  esac
}

# write_private_file SRC DEST — DEST ends up mode 600 on every run. cp would
# keep an existing DEST's mode (e.g. 0644), so write a same-directory temp
# file (mktemp creates it 0600), chmod it, and rename it over DEST.
write_private_file() {
  dest_dir="$(dirname "$2")"
  tmp="$(mktemp "${dest_dir}/.k8scenter-remote-registration.XXXXXX")" ||
    die "could not create a temp file in ${dest_dir}"
  if cat "$1" >"$tmp" && chmod 600 "$tmp" && mv -f "$tmp" "$2"; then
    return 0
  fi
  rm -f "$tmp"
  die "could not write ${2}"
}

register_cluster() {
  api_url="$1"
  log "minting a token for ${FIXTURE_NS}/${PROBE_SA}"
  # Straight to an owner-only file: the token never passes through argv.
  token_file="$TMP_DIR/probe-token"
  rk -n "$FIXTURE_NS" create token "$PROBE_SA" --duration=24h >"$token_file"
  [ -s "$token_file" ] || die "minting the probe token returned nothing"
  ca_file="$TMP_DIR/ca.crt"
  rk config view --raw --minify --flatten \
    -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' |
    base64 -d >"$ca_file" 2>/dev/null || true
  [ -s "$ca_file" ] ||
    die "context ${CONTEXT} carries no inline CA; registration requires the real CA bundle"

  req="$TMP_DIR/register.json"
  jq -n \
    --arg name "$CLUSTER_NAME" \
    --arg url "$api_url" \
    --rawfile ca "$ca_file" \
    --rawfile token "$token_file" \
    '{name: $name, displayName: "E2E remote fixture", apiServerUrl: $url,
      caCert: $ca, token: ($token | rtrimstr("\n")), allowInsecureTLS: false}' >"$req"
  rm -f "$token_file"

  if [ -n "${KUBECENTER_REMOTE_REGISTRATION_FILE:-}" ]; then
    write_private_file "$req" "$KUBECENTER_REMOTE_REGISTRATION_FILE"
    log "registration body written to ${KUBECENTER_REMOTE_REGISTRATION_FILE} (mode 600; contains a token)"
  fi

  log "registering ${api_url} with k8sCenter at ${KUBECENTER_URL}"
  resp="$(api POST /api/v1/clusters "$req")" || die "cluster registration failed"
  id="$(printf '%s' "$resp" | jq -r '.data.id // empty')"
  [ -n "$id" ] || die "registration returned no cluster id: ${resp}"
  printf 'K8SCENTER_REMOTE_CLUSTER_ID=%s\n' "$id"
}

create() {
  need kubectl
  need curl
  need jq
  resolve_context

  if [ -z "$REMOTE_CONTEXT" ]; then
    create_kind_cluster
    api_url="${KUBECENTER_REMOTE_API_URL:-https://${KUBECENTER_REMOTE_API_HOST}:${KIND_PORT}}"
  else
    kubectl config get-contexts "$CONTEXT" >/dev/null 2>&1 ||
      die "kube context '${CONTEXT}' not found"
    api_url="${KUBECENTER_REMOTE_API_URL:-$(rk config view --minify \
      -o jsonpath='{.clusters[0].cluster.server}')}"
  fi

  host="$(printf '%s' "$api_url" | sed -e 's|^[a-z]*://||' -e 's|/.*$||' -e 's|:[0-9]*$||')"
  check_public_host "$host"

  login
  # The identity k8sCenter impersonates for this admin on the remote cluster.
  me="$(api GET /api/v1/auth/me)" || die "could not read the admin's identity"
  user="$(printf '%s' "$me" | jq -r '.data.user.kubernetesUsername // empty')"
  [ -n "$user" ] || die "the admin account has no Kubernetes username to bind"
  # k8sCenter impersonates exactly these groups on every remote request
  # (ClusterRouter.buildRemoteConfig). system:authenticated is always added,
  # so the list is never empty — empty resourceNames would mean ALL groups.
  groups_json="$(printf '%s' "$me" |
    jq -c '((.data.user.kubernetesGroups // []) + ["system:authenticated"]) | unique')"
  case "$groups_json" in
    *'"system:masters"'*)
      die "the admin's Kubernetes groups include system:masters; the fixture will not let its probe token impersonate cluster-admin. Use an admin without that group." ;;
  esac

  apply_fixture_objects "$user" "$groups_json"
  register_cluster "$api_url"
}

case "${1:-}" in
  --teardown) teardown ;;
  "") create ;;
  -h | --help) sed -n '2,/^$/p' "$0" ;;
  *) die "unknown argument: $1 (use --teardown or --help)" ;;
esac
