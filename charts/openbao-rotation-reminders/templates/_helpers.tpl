{{- define "openbao-rotation-reminders.name" -}}
{{- regexReplaceAll "[-._]+$" (default .Chart.Name .Values.nameOverride | trunc 63) "" -}}
{{- end -}}

{{- define "openbao-rotation-reminders.fullname" -}}
{{- $full := .Values.fullnameOverride -}}
{{- if not $full -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- $full = .Release.Name -}}
{{- else -}}
{{- $full = printf "%s-%s" .Release.Name $name -}}
{{- end -}}
{{- end -}}
{{- if gt (len $full) 52 -}}
{{- $hash := sha256sum $full | trunc 8 -}}
{{- $prefix := regexReplaceAll "[-.]+$" ($full | trunc 43) "" -}}
{{- if $prefix -}}
{{- printf "%s-%s" $prefix $hash -}}
{{- else -}}
{{- $hash -}}
{{- end -}}
{{- else -}}
{{- regexReplaceAll "[-.]+$" $full "" -}}
{{- end -}}
{{- end -}}

{{- define "openbao-rotation-reminders.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openbao-rotation-reminders.name" . | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "openbao-rotation-reminders.labels" -}}
helm.sh/chart: {{ regexReplaceAll "[-._]+$" (printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63) "" | quote }}
{{ include "openbao-rotation-reminders.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
{{- end -}}

{{- define "openbao-rotation-reminders.podLabels" -}}
{{- $labels := include "openbao-rotation-reminders.labels" . | fromYaml -}}
{{- $selector := include "openbao-rotation-reminders.selectorLabels" . | fromYaml -}}
{{- toYaml (mergeOverwrite $labels (deepCopy (default dict .Values.podLabels)) $selector) -}}
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
