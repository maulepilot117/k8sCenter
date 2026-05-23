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

{{- /* ── externalDatabase password ────────────────────────────────── */}}
{{- if .Values.externalDatabase.host -}}
{{- if and .Values.externalDatabase.password (hasPrefix "replace_me_" (lower .Values.externalDatabase.password)) -}}
{{- fail "externalDatabase.password contains a REPLACE_ME placeholder. Replace it before deploying. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.externalDatabase.password (has .Values.externalDatabase.password $knownLeaked) -}}
{{- fail "externalDatabase.password matches a value committed to the repository before the 2026-05-22 security audit (Finding P0-1). Generate a fresh password." -}}
{{- end -}}
{{- end -}}

{{- end -}}
