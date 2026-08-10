{{/*
NetworkPolicy — off by default. `networkPolicy: true` renders a default-deny
ingress policy; allow rules are layered on top.
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
