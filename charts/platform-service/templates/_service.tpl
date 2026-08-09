{{/*
Service — one per service, exposing the workload on port 80 and forwarding to
the container's http port. Kept minimal: every service on this platform speaks
HTTP and is consumed over the mesh, so the service abstraction never needs to
vary.

Named template — a service chart calls it via
`include "platform-service.service" .`.
*/}}
{{- define "platform-service.service" -}}
{{- range $name, $svc := .Values.services }}
{{- $lbls := dict "svc" (set $svc "name" $name) "root" $ -}}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ $name }}
  namespace: {{ $.Values.namespace | default "demo" }}
  labels:
    {{- include "platform-service.labels" $lbls | nindent 4 }}
spec:
  selector:
    {{- include "platform-service.selectorLabels" (dict "svc" (dict "name" $name)) | nindent 4 }}
  ports:
    - name: http
      port: 80
      targetPort: http
{{- end }}
{{- end -}}
