{{- define "fleetpulse-agent.name" -}}
fleetpulse-agent
{{- end -}}

{{- define "fleetpulse-agent.labels" -}}
app.kubernetes.io/name: {{ include "fleetpulse-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
