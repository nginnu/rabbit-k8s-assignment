{{/*
Workload — Deployment or Rollout from the same template.

`workloadKind` selects which. Deployment is the default; setting Rollout
switches a service to Argo Rollouts for canary or blue-green delivery without
editing a template, because the only structural difference between the two is
the apiVersion and the strategy block, both handled below. Leaving that seam
open now is what makes the change one line later.

Named template — a service chart calls it via
`include "platform-service.deployment-rollout" .`.
*/}}
{{- define "platform-service.deployment-rollout" -}}
{{- range $name, $svc := .Values.services }}
{{- $lbls := dict "svc" (set $svc "name" $name) "root" $ -}}
---
apiVersion: {{ if eq $svc.workloadKind "Rollout" }}argoproj.io/v1alpha1{{ else }}apps/v1{{ end }}
kind: {{ $svc.workloadKind | default "Deployment" }}
metadata:
  name: {{ $name }}
  namespace: {{ $.Values.namespace | default "demo" }}
  labels:
    {{- include "platform-service.labels" $lbls | nindent 4 }}
spec:
  replicas: {{ $svc.replicas | default 1 }}
  {{- if ne $svc.workloadKind "Rollout" }}
  {{/*
    Stated rather than left to the API server's defaults. An unmentioned field
    stays owned by whoever wrote it first, so a Deployment created by kubectl
    keeps ownership of strategy and blocks Helm from ever adopting the object —
    the migration fails on a field the chart never had an opinion about.

    The values are the Kubernetes defaults; declaring them changes nothing
    about how a rollout behaves, only who owns the fields.

    Rollout has its own strategy block and must not receive this one.
  */}}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: {{ $svc.maxSurge | default "25%" }}
      maxUnavailable: {{ $svc.maxUnavailable | default "25%" }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "platform-service.selectorLabels" (dict "svc" (dict "name" $name)) | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "platform-service.labels" $lbls | nindent 8 }}
    spec:
      {{- if $svc.topologySpread }}
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: {{ $name }}
      {{- end }}
      containers:
        - name: {{ $name }}
          image: {{ include "platform-service.image" $lbls }}
          imagePullPolicy: {{ $svc.image.pullPolicy | default "IfNotPresent" }}
          ports:
            - name: http
              containerPort: {{ $svc.port }}
          {{- with $.Values.sharedConfig }}
          envFrom:
            - configMapRef:
                name: {{ .name }}
          {{- end }}
          env:
            {{- with $svc.env }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
            {{- range $key := $svc.envFromSecret }}
            - name: {{ $key | upper | replace "-" "_" }}
              valueFrom:
                secretKeyRef:
                  name: {{ $.Values.sharedSecret.name }}
                  key: {{ $key }}
            {{- end }}
          {{- with $svc.probes }}
          {{/*
            hasKey rather than `default` on the delays: Go templates treat 0 as
            empty, so `default 10` silently rewrites an explicit "start probing
            immediately" into a ten second wait. A service that answers at once
            has no way to say so otherwise.
          */}}
          livenessProbe:
            httpGet:
              path: {{ .liveness.path | default "/healthz" }}
              port: http
            initialDelaySeconds: {{ if hasKey .liveness "initialDelaySeconds" }}{{ .liveness.initialDelaySeconds }}{{ else }}10{{ end }}
            periodSeconds: {{ .liveness.periodSeconds | default 15 }}
          readinessProbe:
            httpGet:
              path: {{ .readiness.path | default "/healthz" }}
              port: http
            initialDelaySeconds: {{ if hasKey .readiness "initialDelaySeconds" }}{{ .readiness.initialDelaySeconds }}{{ else }}5{{ end }}
            periodSeconds: {{ .readiness.periodSeconds | default 5 }}
          {{- end }}
          resources:
            {{- toYaml $svc.resources | nindent 12 }}
          securityContext:
            {{- include "platform-service.securityContext" (dict "svc" $svc) | nindent 12 }}
  {{- if eq $svc.workloadKind "Rollout" }}
  strategy:
    {{- toYaml $svc.rollout.strategy | nindent 4 }}
  {{- else }}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  {{- end }}
{{- end }}
{{- end -}}
