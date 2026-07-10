{{- define "waas.name" -}}
{{ .Release.Name }}
{{- end }}

{{- define "waas.labels" -}}
app.kubernetes.io/part-of: waas
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end }}

{{- define "waas.tag" -}}
{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/* CR namespace: defaults to the release namespace (same place the
platform pods run) unless workspaces.namespace is set explicitly. */}}
{{- define "waas.workspacesNamespace" -}}
{{ .Values.workspaces.namespace | default .Release.Namespace }}
{{- end }}

{{/*
Scrape annotations for annotation-based Prometheus discovery. Usage:
  {{ include "waas.scrapeAnnotations" (dict "root" $ "port" 8080) }}
Emits nothing unless BOTH metrics.enabled and metrics.scrapeAnnotations
are set (annotations pointing at a disabled endpoint would only create
scrape errors).
*/}}
{{- define "waas.scrapeAnnotations" -}}
{{- if and .root.Values.metrics.enabled .root.Values.metrics.scrapeAnnotations -}}
prometheus.io/scrape: "true"
prometheus.io/port: {{ .port | quote }}
prometheus.io/path: /metrics
{{- end }}
{{- end }}

{{/* Secrets are generated once and reused across upgrades via lookup. */}}
{{- define "waas.existingSecret" -}}
{{ (lookup "v1" "Secret" .Release.Namespace (printf "%s-secrets" .Release.Name)).data | default dict | toJson }}
{{- end }}
