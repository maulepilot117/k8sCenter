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
#   - ServiceAccount k8scenter-remote-probe with `impersonate` on users and
#     groups. k8sCenter's connection test (ProbeImpersonateRights) refuses a
#     registration without users; groups are needed because every remote
#     request impersonates the user's groups as well;
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
#   KUBECENTER_REMOTE_CLUSTER_ID with --teardown, also deregister this id
#   KUBECENTER_REMOTE_REGISTRATION_FILE
#                                if set, also write the registration request
#                                body there (mode 600). It holds the probe
#                                token, so treat it as a credential. The e2e
#                                spec's delete-and-re-register case replays it
#                                and skips when it is absent.
#
# Teardown deletes ONLY objects carrying the fixture label, selected by label,
# never by name — so running it against a shared cluster cannot remove
# anything the fixture did not create. The kind cluster is deleted only when
# this script created it (KUBECENTER_REMOTE_CONTEXT unset).
#
# Requires: kubectl, curl, jq; kind unless KUBECENTER_REMOTE_CONTEXT is set.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

FIXTURE_LABEL_KEY="app.kubernetes.io/managed-by"
FIXTURE_LABEL_VALUE="k8scenter-e2e-remote-fixture"
FIXTURE_SELECTOR="${FIXTURE_LABEL_KEY}=${FIXTURE_LABEL_VALUE}"
FIXTURE_NS="k8scenter-remote-fixture"
PROBE_SA="k8scenter-remote-probe"
KIND_NAME="k8scenter-remote"
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
      log "deleting kind cluster ${KIND_NAME}"
      kind delete cluster --name "$KIND_NAME"
    fi
  fi
  log "teardown complete"
}

# ------------------------------------------------------------- k8sCenter API --

ACCESS_TOKEN=""

login() {
  need curl
  need jq
  [ -n "${KUBECENTER_ADMIN_USER:-}" ] || die "KUBECENTER_ADMIN_USER is required"
  [ -n "${KUBECENTER_ADMIN_PASSWORD:-}" ] || die "KUBECENTER_ADMIN_PASSWORD is required"
  body="$(jq -n --arg u "$KUBECENTER_ADMIN_USER" --arg p "$KUBECENTER_ADMIN_PASSWORD" \
    '{username: $u, password: $p}')"
  # Login shares the 5 req/min per-IP auth bucket, so this runs once.
  resp="$(curl -sS -X POST "${KUBECENTER_URL}/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
    -d "$body")" || die "login request failed"
  ACCESS_TOKEN="$(printf '%s' "$resp" | jq -r '.data.accessToken // empty')"
  [ -n "$ACCESS_TOKEN" ] || die "login failed: $(printf '%s' "$resp" | jq -c '.error // .')"
}

# api METHOD PATH [JSON-FILE] — prints the body, fails on a non-2xx status.
api() {
  out="$TMP_DIR/api-response"
  if [ $# -ge 3 ]; then
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$1" "${KUBECENTER_URL}$2" \
      -H "Authorization: Bearer ${ACCESS_TOKEN}" \
      -H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
      --data-binary "@$3")"
  else
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$1" "${KUBECENTER_URL}$2" \
      -H "Authorization: Bearer ${ACCESS_TOKEN}" \
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
    log "kind cluster ${KIND_NAME} already exists; reusing it"
    return
  fi
  cfg="$TMP_DIR/remote-kind-config.yaml"
  # Substitute only the one placeholder; the rest of the file is literal.
  sed "s|\${KUBECENTER_REMOTE_API_HOST}|${KUBECENTER_REMOTE_API_HOST}|" \
    "$ROOT/e2e/remote-kind-config.yaml" >"$cfg"
  log "creating kind cluster ${KIND_NAME}"
  kind create cluster --config "$cfg" --wait 120s
}

apply_fixture_objects() {
  user="$1"
  log "applying fixture objects to ${CONTEXT} for impersonated user '${user}'"
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
  - apiGroups: [""]
    resources: ["users", "groups"]
    verbs: ["impersonate"]
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

register_cluster() {
  api_url="$1"
  log "minting a token for ${FIXTURE_NS}/${PROBE_SA}"
  token="$(rk -n "$FIXTURE_NS" create token "$PROBE_SA" --duration=24h)"
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
    --arg token "$token" \
    '{name: $name, displayName: "E2E remote fixture", apiServerUrl: $url,
      caCert: $ca, token: $token, allowInsecureTLS: false}' >"$req"

  if [ -n "${KUBECENTER_REMOTE_REGISTRATION_FILE:-}" ]; then
    (umask 077 && cp "$req" "$KUBECENTER_REMOTE_REGISTRATION_FILE")
    log "registration body written to ${KUBECENTER_REMOTE_REGISTRATION_FILE} (contains a token)"
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

  apply_fixture_objects "$user"
  register_cluster "$api_url"
}

case "${1:-}" in
  --teardown) teardown ;;
  "") create ;;
  -h | --help) sed -n '2,62p' "$0" ;;
  *) die "unknown argument: $1 (use --teardown or --help)" ;;
esac
