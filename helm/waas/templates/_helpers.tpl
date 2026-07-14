{{- define "waas.name" -}}
{{ .Release.Name }}
{{- end }}

{{- define "waas.labels" -}}
app.kubernetes.io/part-of: waas
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- with .Values.global.labels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Pod-level labels: global.labels merged with the component's own
podLabels (the static app.kubernetes.io/name|instance selector labels
are NOT part of this — they're written directly at each callsite so
they can never be overridden by a stray global/podLabels key with the
same name). Usage:
  {{ include "waas.podLabels" (dict "root" $ "podLabels" .Values.operator.podLabels) }}
Returns "" when both are empty.
*/}}
{{- define "waas.podLabels" -}}
{{- $merged := mergeOverwrite (dict) (.root.Values.global.labels | default dict) (.podLabels | default dict) -}}
{{- if $merged -}}
{{ toYaml $merged }}
{{- end -}}
{{- end -}}

{{/*
Deployment/StatefulSet-level annotations: global.annotations merged with
the component's own deploymentAnnotations. Usage:
  {{ include "waas.deploymentAnnotations" (dict "root" $ "annotations" .Values.operator.deploymentAnnotations) }}
Returns "" (nothing to render) when both are empty.
*/}}
{{- define "waas.deploymentAnnotations" -}}
{{- $merged := mergeOverwrite (dict) (.root.Values.global.annotations | default dict) (.annotations | default dict) -}}
{{- if $merged -}}
{{ toYaml $merged }}
{{- end -}}
{{- end -}}

{{/*
Pod-level annotations: global.annotations, the component's own
podAnnotations, and (only when "port" is given) the scrapeAnnotations
toggle — merged into one map. Usage:
  {{ include "waas.podAnnotations" (dict "root" $ "podAnnotations" .Values.operator.podAnnotations "port" 8080) }}
Omit "port" for components with no /metrics endpoint. Returns "" when
nothing applies.
*/}}
{{- define "waas.podAnnotations" -}}
{{- $merged := mergeOverwrite (dict) (.root.Values.global.annotations | default dict) (.podAnnotations | default dict) -}}
{{- if and .port .root.Values.metrics.enabled .root.Values.metrics.scrapeAnnotations -}}
{{- $merged = mergeOverwrite $merged (dict "prometheus.io/scrape" "true" "prometheus.io/port" (.port | toString) "prometheus.io/path" "/metrics") -}}
{{- end -}}
{{- if $merged -}}
{{ toYaml $merged }}
{{- end -}}
{{- end -}}

{{- define "waas.tag" -}}
{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/* CR namespace: defaults to the release namespace (same place the
platform pods run) unless workspaces.namespace is set explicitly. */}}
{{- define "waas.workspacesNamespace" -}}
{{ .Values.workspaces.namespace | default .Release.Namespace }}
{{- end }}
