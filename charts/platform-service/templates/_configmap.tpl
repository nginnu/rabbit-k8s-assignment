{{/*
Shared ConfigMap — non-secret environment that more than one service reads.

One object rather than a copy per service: values belonging to the environment
are not properties of any single workload, and duplicating them means every
future change has to find all the copies.

Rendered only when .Values.sharedConfig is set, so a release with no shared
environment produces no empty object.

Named template — a service chart calls it via
`include "platform-service.configmap" .`.
*/}}
{{- define "platform-service.configmap" -}}
{{- with .Values.sharedConfig }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .name }}
  namespace: {{ $.Values.namespace | default "demo" }}
  labels:
    app.kubernetes.io/part-of: platform
    app.kubernetes.io/managed-by: {{ $.Release.Service }}
data:
  {{- range $k, $v := .data }}
  {{ $k }}: {{ $v | quote }}
  {{- end }}
{{- end }}
{{- end -}}
