{{- define "platform-service.httproute" -}}
{{- range $name, $svc := .Values.services }}
{{- if $svc.route }}
{{- $lbls := dict "svc" (set $svc "name" $name) "root" $ -}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ $name }}
  namespace: {{ include "platform-service.namespace" $ }}
  labels:
    {{- include "platform-service.labels" $lbls | nindent 4 }}
spec:
  hostnames:
    {{- range $svc.route.hostnames }}
    - {{ . }}
    {{- end }}
  parentRefs:
    - name: {{ $svc.route.gateway | default "external" }}
      namespace: {{ $svc.route.gatewayNamespace | default "gateway" }}
      {{- with $svc.route.section }}
      sectionName: {{ . }}
      {{- end }}
  rules:
    {{- range $svc.route.rules }}
    - matches:
        - path: { type: PathPrefix, value: {{ .path }} }
      {{- with .rewrite }}
      filters:
        - type: URLRewrite
          urlRewrite:
            path: { type: ReplacePrefixMatch, replacePrefixMatch: {{ . }} }
      {{- end }}
      backendRefs:
        - name: {{ $name }}
          port: 80
    {{- end }}
{{- end }}
{{- end }}
{{- end -}}
