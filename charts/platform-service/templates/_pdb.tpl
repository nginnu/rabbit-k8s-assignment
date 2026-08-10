{{/*
PodDisruptionBudget — rendered only when a service declares one. It has teeth
because the cluster has more than one node to reschedule onto.
*/}}
{{- define "platform-service.pdb" -}}
{{- range $name, $svc := .Values.services }}
{{- with $svc.pdb }}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ $name }}
  namespace: {{ $.Values.namespace | default "demo" }}
spec:
  {{- toYaml . | nindent 2 }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ $name }}
{{- end }}
{{- end }}
{{- end -}}
