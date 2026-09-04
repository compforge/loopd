{{- define "loopd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "loopd.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "loopd.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "loopd.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "loopd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "loopd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "loopd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "loopd.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "loopd.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "loopd.serverName" -}}
{{ include "loopd.fullname" . }}-server
{{- end }}

{{- define "loopd.webName" -}}
{{ include "loopd.fullname" . }}-web
{{- end }}

{{- define "loopd.routerName" -}}
{{ include "loopd.fullname" . }}-router
{{- end }}

{{- define "loopd.routerConfigName" -}}
{{ include "loopd.routerName" . }}-model
{{- end }}

{{- define "loopd.redisName" -}}
{{ include "loopd.fullname" . }}-redis
{{- end }}

{{- define "loopd.redisAddress" -}}
{{- if .Values.redis.address -}}
{{ .Values.redis.address }}
{{- else -}}
{{ printf "%s:6379" (include "loopd.redisName" .) }}
{{- end -}}
{{- end }}

{{- define "loopd.routerSecretName" -}}
{{- default (printf "%s-model-secret" (include "loopd.routerName" .)) .Values.router.model.existingSecret -}}
{{- end }}
