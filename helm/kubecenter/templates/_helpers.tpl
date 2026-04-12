{{/*
Expand the name of the chart.
*/}}
{{- define "kubecenter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kubecenter.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels

The app.kubernetes.io/version label must be a valid Kubernetes label value
(RFC 1123 subdomain, ≤63 chars, alphanumeric + [-_.]). argocd-image-updater's
`digest` strategy rewrites image tags to `latest@sha256:<digest>`, which is
88 chars and contains `@` and `:` — both illegal. Strip anything after `@`
and truncate to 63 chars so the label stays valid regardless of which
update strategy is in use.
*/}}
{{- define "kubecenter.labels" -}}
{{- $rawVersion := .Values.backend.image.tag | default .Chart.AppVersion -}}
{{- $version := $rawVersion | toString | splitList "@" | first | trunc 63 | trimSuffix "-" | trimSuffix "." | trimSuffix "_" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "kubecenter.selectorLabels" . }}
app.kubernetes.io/version: {{ $version | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubecenter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubecenter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{/*
Frontend fullname
*/}}
{{- define "kubecenter.frontendFullname" -}}
{{ include "kubecenter.fullname" . }}-frontend
{{- end }}

{{/*
Service account name
*/}}
{{- define "kubecenter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubecenter.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
