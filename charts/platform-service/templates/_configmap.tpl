{{/*
Shared ConfigMap — non-secret environment more than one service reads.

Rendered only when .Values.sharedConfig is set.
*/}}
{{- define "platform-service.configmap" -}}
{{- with .Values.sharedConfig }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .name }}
  namespace: {{ include "platform-service.namespace" $ }}
  labels:
    app.kubernetes.io/part-of: platform
    app.kubernetes.io/managed-by: {{ $.Release.Service }}
data:
  {{- range $k, $v := .data }}
  {{ $k }}: {{ $v | quote }}
  {{- end }}
{{- end }}
{{- end -}}
