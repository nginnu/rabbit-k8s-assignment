{{/*
NetworkPolicy — off by default, declared here so the schema is complete and a
service can opt in without a template change later.

When `networkPolicy: true`, a default-deny ingress policy is rendered. The
allow-list — which services may talk to which — is layered on top of the deny
once there is more than one service to isolate from another; a deny with
nothing to permit is the honest starting point.

Named template — a service chart calls it via
`include "platform-service.networkpolicy" .`.
*/}}
{{- define "platform-service.networkpolicy" -}}
{{- range $name, $svc := .Values.services }}
{{- if $svc.networkPolicy }}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ $name }}-default-deny
  namespace: {{ $.Values.namespace | default "demo" }}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{ $name }}
  policyTypes:
    - Ingress
  ingress: []
{{- end }}
{{- end }}
{{- end -}}
