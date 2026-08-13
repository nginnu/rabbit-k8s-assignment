{{/*
Workload — Deployment or Rollout from the same template.

`workloadKind` selects which. The only structural difference is the apiVersion
and the strategy block, so switching a service to canary is one line in values.
*/}}
{{- define "platform-service.deployment-rollout" -}}
{{- range $name, $svc := .Values.services }}
{{- $lbls := dict "svc" (set $svc "name" $name) "root" $ -}}
---
apiVersion: {{ if eq $svc.workloadKind "Rollout" }}argoproj.io/v1alpha1{{ else }}apps/v1{{ end }}
kind: {{ $svc.workloadKind | default "Deployment" }}
metadata:
  name: {{ $name }}
  namespace: {{ include "platform-service.namespace" $ }}
  labels:
    {{- include "platform-service.labels" $lbls | nindent 4 }}
spec:
  replicas: {{ $svc.replicas | default 1 }}
  {{- if ne $svc.workloadKind "Rollout" }}
  {{/*
    Stated, not left to the API server. An unmentioned field stays owned by
    whoever wrote it first, so a Deployment created by kubectl keeps ownership
    of strategy and Helm can never adopt the object. Rollout has its own
    strategy block below and must not get this one.
  */}}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      {{/*
        maxUnavailable 0 rather than the Kubernetes default of 25%: a new pod
        has to be ready before an old one goes, so a rollout never runs below
        the declared replica count.
      */}}
      maxSurge: {{ $svc.maxSurge | default 1 }}
      maxUnavailable: {{ $svc.maxUnavailable | default 0 }}
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
            hasKey, not `default`, for the delays: Go templates treat 0 as empty,
            so `default 10` would rewrite an explicit 0 into a ten second wait.

            timeoutSeconds is stated because the Kubernetes default of 1s fails
            a probe on a pod that is busy but healthy.
          */}}
          livenessProbe:
            httpGet:
              path: {{ .liveness.path | default "/healthz" }}
              port: http
            initialDelaySeconds: {{ if hasKey .liveness "initialDelaySeconds" }}{{ .liveness.initialDelaySeconds }}{{ else }}10{{ end }}
            periodSeconds: {{ .liveness.periodSeconds | default 15 }}
            timeoutSeconds: {{ .liveness.timeoutSeconds | default 3 }}
            failureThreshold: {{ .liveness.failureThreshold | default 3 }}
          readinessProbe:
            httpGet:
              path: {{ .readiness.path | default "/healthz" }}
              port: http
            initialDelaySeconds: {{ if hasKey .readiness "initialDelaySeconds" }}{{ .readiness.initialDelaySeconds }}{{ else }}5{{ end }}
            periodSeconds: {{ .readiness.periodSeconds | default 5 }}
            timeoutSeconds: {{ .readiness.timeoutSeconds | default 3 }}
            failureThreshold: {{ .readiness.failureThreshold | default 3 }}
          {{- end }}
          resources:
            {{- toYaml $svc.resources | nindent 12 }}
          securityContext:
            {{- include "platform-service.securityContext" (dict "svc" $svc) | nindent 12 }}
  {{- if eq $svc.workloadKind "Rollout" }}
  strategy:
    {{- toYaml $svc.rollout.strategy | nindent 4 }}
  {{- end }}
{{- end }}
{{- end -}}
