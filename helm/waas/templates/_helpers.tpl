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

{{/* Secrets are generated once and reused across upgrades via lookup. */}}
{{- define "waas.existingSecret" -}}
{{ (lookup "v1" "Secret" .Release.Namespace (printf "%s-secrets" .Release.Name)).data | default dict | toJson }}
{{- end }}
