{{/*
Service — port 80 forwarding to the container's http port.
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
