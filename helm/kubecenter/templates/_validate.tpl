{{/*
kubecenter.validatePlaceholders — fail-fast guard for placeholder and
known-leaked credential values.

Called from secret-app.yaml (which always renders) so that helm template /
helm install / helm upgrade all abort before emitting any Kubernetes objects.

Finding references: P0-1 (committed secrets), P1-1 (auto-gen setup token).
Security audit: 2026-05-22.
*/}}
{{- define "kubecenter.validatePlaceholders" -}}
{{- $knownLeaked := list "homelab-jwt-secret-for-k8scenter-minimum-32-bytes" "homelab-setup-token" "k8sC3nterDB2026" -}}

{{- /* ── JWT secret ─────────────────────────────────────────────────── */}}
{{- if and .Values.auth.jwtSecret (hasPrefix "REPLACE_ME_" .Values.auth.jwtSecret) -}}
{{- fail "auth.jwtSecret contains a REPLACE_ME placeholder. Replace it with a generated secret (>=32 bytes) before deploying. See values-homelab.yaml.example. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.auth.jwtSecret (has .Values.auth.jwtSecret $knownLeaked) -}}
{{- fail "auth.jwtSecret matches a value committed to the repository before the 2026-05-22 security audit (Finding P0-1). Generate a fresh secret." -}}
{{- end -}}

{{- /* ── Setup token ──────────────────────────────────────────────────── */}}
{{- if and .Values.auth.setupToken (hasPrefix "REPLACE_ME_" .Values.auth.setupToken) -}}
{{- fail "auth.setupToken contains a REPLACE_ME placeholder. Replace it before deploying, or leave empty to let the chart auto-generate one. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.auth.setupToken (has .Values.auth.setupToken $knownLeaked) -}}
{{- fail "auth.setupToken matches a known-leaked value (Finding P0-1). Generate a fresh token." -}}
{{- end -}}

{{- /* ── Postgres password (when postgresql subchart is enabled) ────── */}}
{{- if .Values.postgresql.enabled -}}
{{- if and .Values.postgresql.auth.password (hasPrefix "REPLACE_ME_" .Values.postgresql.auth.password) -}}
{{- fail "postgresql.auth.password contains a REPLACE_ME placeholder. Replace it before deploying. Finding P0-1." -}}
{{- end -}}
{{- if and .Values.postgresql.auth.password (has .Values.postgresql.auth.password $knownLeaked) -}}
{{- fail "postgresql.auth.password matches a known-leaked value (Finding P0-1). Generate a fresh password." -}}
{{- end -}}
{{- end -}}

{{- end -}}
