{{/*
kubecenter.validatePlaceholders — fail-fast guard for placeholder and
known-leaked credential values.

Included from EVERY rendered template (not just secret-app.yaml) so that
helm template --show-only <any-template> also aborts on placeholder values.
The helper is idempotent — including it multiple times still fail-fasts on
the first hit.

Finding references: P0-1 (committed secrets), P1-1 (auto-gen setup token),
  P2-9 (--show-only bypass), P2-11 (externalDatabase.password gap),
  P3-17 (case-sensitive REPLACE_ME), P3-22 (reciprocal sync comment).
Security audit: 2026-05-22.

Keep $knownLeaked list in sync with backend/internal/config/known_leaked.go (Finding P0-1).
The parity test in backend/internal/config/known_leaked_helm_parity_test.go enforces this.
*/}}
{{- define "kubecenter.validatePlaceholders" -}}
{{- $knownLeaked := list "homelab-jwt-secret-for-k8scenter-minimum-32-bytes" "homelab-setup-token" "k8sC3nterDB2026" -}}

{{- /* ── JWT secret ─────────────────────────────────────────────────── */}}
{{- if and .Values.auth.jwtSecret (hasPrefix "replace_me_" (lower .Values.auth.jwtSecret)) -}}
{{- fail "auth.jwtSecret contains a REPLACE_ME placeholder. Replace it with a generated secret (>=32 bytes) before deploying. See values-homelab.yaml.example. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.auth.jwtSecret (has .Values.auth.jwtSecret $knownLeaked) -}}
{{- fail "auth.jwtSecret matches a value committed to the repository before the 2026-05-22 security audit (Finding P0-1). Generate a fresh secret." -}}
{{- end -}}

{{- /* ── Setup token ──────────────────────────────────────────────────── */}}
{{- if and .Values.auth.setupToken (hasPrefix "replace_me_" (lower .Values.auth.setupToken)) -}}
{{- fail "auth.setupToken contains a REPLACE_ME placeholder. Replace it before deploying, or leave empty to let the chart auto-generate one. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.auth.setupToken (has .Values.auth.setupToken $knownLeaked) -}}
{{- fail "auth.setupToken matches a known-leaked value (Finding P0-1). Generate a fresh token." -}}
{{- end -}}

{{- /* ── Postgres password (when postgresql subchart is enabled) ────── */}}
{{- if .Values.postgresql.enabled -}}
{{- if and .Values.postgresql.auth.password (hasPrefix "replace_me_" (lower .Values.postgresql.auth.password)) -}}
{{- fail "postgresql.auth.password contains a REPLACE_ME placeholder. Replace it before deploying. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.postgresql.auth.password (has .Values.postgresql.auth.password $knownLeaked) -}}
{{- fail "postgresql.auth.password matches a known-leaked value (Finding P0-1). Generate a fresh password." -}}
{{- end -}}
{{- end -}}

{{- /* ── Image tags (P3-9 2026-05-22 audit) ──────────────────────────────
The values-homelab.yaml.example file now uses REPLACE_ME_*_TAG
placeholders for backend.image.tag and frontend.image.tag instead of
the previous `latest` defaults. The CHANGELOG promises this guard
refuses to render until the operator picks a real tag — match that
promise here so a fresh homelab install does not deploy pods that
ImagePullBackOff on the placeholder string. Audit finding P3-9 +
Phase 7 ce-code-review AN-1.
*/}}
{{- if and .Values.backend.image.tag (hasPrefix "replace_me_" (lower .Values.backend.image.tag)) -}}
{{- fail "backend.image.tag contains a REPLACE_ME placeholder. Replace it with a versioned tag (e.g. v0.42.0) or a sha-<commit> build tag before deploying. See values-homelab.yaml.example. Finding P3-9." -}}
{{- end -}}
{{- if and .Values.frontend.image.tag (hasPrefix "replace_me_" (lower .Values.frontend.image.tag)) -}}
{{- fail "frontend.image.tag contains a REPLACE_ME placeholder. Replace it with a versioned tag (e.g. v0.42.0) or a sha-<commit> build tag before deploying. See values-homelab.yaml.example. Finding P3-9." -}}
{{- end -}}

{{- /* ── externalDatabase password ────────────────────────────────── */}}
{{- if .Values.externalDatabase.host -}}
{{- if and .Values.externalDatabase.password (hasPrefix "replace_me_" (lower .Values.externalDatabase.password)) -}}
{{- fail "externalDatabase.password contains a REPLACE_ME placeholder. Replace it before deploying. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.externalDatabase.password (has .Values.externalDatabase.password $knownLeaked) -}}
{{- fail "externalDatabase.password matches a value committed to the repository before the 2026-05-22 security audit (Finding P0-1). Generate a fresh password." -}}
{{- end -}}
{{- end -}}

{{- /* ── TLS-by-default exposure guard (P2-8) ──────────────────────────
The previous chart let an operator expose the backend over plaintext
HTTP by enabling `ingress.enabled` without `ingress.tls`, or by
flipping `service.type` to LoadBalancer / NodePort. Either path leaks
access tokens, refresh tokens, setup tokens, and API traffic in the
clear. Dev mode (`backend.config.dev=true`) compounds the issue: it
relaxes the cookie Secure flag, so an exposed dev deployment ships
session cookies recoverable over the wire.

The chart now refuses to render if any of those exposure modes are
configured without an explicit acknowledgement via
`security.insecureExposureAcknowledged=true`. Set the override only
for fully internal trust-domain deployments where TLS termination
happens upstream and the network path is private.
*/}}
{{- $sec := .Values.security | default dict }}
{{- /* Phase 4 review (adversarial, reliability R-5): `not` evaluates non-
empty strings as truthy, so `--set-string security.insecureExposureAcknowledged="false"`
slipped past the previous boolean coercion. `toString` + `eq "true"`
matches the dev-mode check below and forces a literal-true comparison. */}}
{{- $ackInsecure := eq (toString ($sec.insecureExposureAcknowledged | default false)) "true" }}
{{- $svcType := .Values.service.type | default "ClusterIP" }}
{{- $ingressOn := .Values.ingress.enabled | default false }}
{{- /* Phase 4 review (adversarial, reliability R-5): a single empty-map
entry like `ingress.tls: [{}]` passes `not (empty .Values.ingress.tls)`
and renders an Ingress with `secretName: ""`. Require at least one
entry with a non-empty secretName. */}}
{{- $ingressHasTLS := false }}
{{- if $ingressOn }}
{{- range .Values.ingress.tls }}
{{- if .secretName }}
{{- $ingressHasTLS = true }}
{{- end }}
{{- end }}
{{- end }}
{{- $devMode := and .Values.backend (and .Values.backend.config (eq (toString .Values.backend.config.dev) "true")) }}

{{- if and $ingressOn (not $ingressHasTLS) -}}
{{- if not $ackInsecure -}}
{{- fail "ingress.enabled=true but ingress.tls has no entry with a non-empty secretName — exposing the backend over plaintext HTTP leaks tokens. Configure ingress.tls with a TLS secret name, or set security.insecureExposureAcknowledged=true to accept the risk (only safe for internal-only deployments behind upstream TLS termination). Finding P2-8." -}}
{{- end -}}
{{- end -}}

{{- if or (eq $svcType "LoadBalancer") (eq $svcType "NodePort") -}}
{{- if not $ackInsecure -}}
{{- fail (printf "service.type=%s exposes the backend on a raw network port without TLS termination — set service.type to ClusterIP and front the chart with an HTTPS ingress, or set security.insecureExposureAcknowledged=true to accept the risk. Finding P2-8." $svcType) -}}
{{- end -}}
{{- end -}}

{{- if $devMode -}}
{{- if or (eq $svcType "LoadBalancer") (eq $svcType "NodePort") (and $ingressOn (not $ingressHasTLS)) -}}
{{- if not $ackInsecure -}}
{{- fail "backend.config.dev=true combined with an externally-reachable service (LoadBalancer/NodePort, or ingress without TLS) — dev mode relaxes cookie Secure flags and rate limits. Use dev mode only with service.type=ClusterIP and no public ingress, or set security.insecureExposureAcknowledged=true to accept the risk. Finding P2-8." -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- end -}}
