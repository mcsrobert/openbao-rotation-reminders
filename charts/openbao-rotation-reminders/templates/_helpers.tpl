{{- define "openbao-rotation-reminders.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "openbao-rotation-reminders.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 52 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 52 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 52 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "openbao-rotation-reminders.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openbao-rotation-reminders.name" . | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "openbao-rotation-reminders.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" | quote }}
{{ include "openbao-rotation-reminders.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
{{- end -}}

{{- define "openbao-rotation-reminders.podLabels" -}}
{{- $selector := include "openbao-rotation-reminders.selectorLabels" . | fromYaml -}}
{{- $labels := include "openbao-rotation-reminders.labels" . | fromYaml -}}
{{- toYaml (merge $selector (deepCopy .Values.podLabels) $labels) -}}
{{- end -}}

{{- define "openbao-rotation-reminders.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "openbao-rotation-reminders.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "openbao-rotation-reminders.image" -}}
{{- $reference := printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" $reference .Values.image.digest -}}
{{- else -}}
{{- $reference -}}
{{- end -}}
{{- end -}}

{{- define "openbao-rotation-reminders.role" -}}
{{- default (include "openbao-rotation-reminders.fullname" .) .Values.openbao.auth.role -}}
{{- end -}}
