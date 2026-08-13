{{/*
AnalysisTemplate — the automatic abort path for a canary Rollout.

Rendered only for a service that declares `rollout.analysis`, so a Deployment
service produces nothing and the Argo Rollouts CRDs stay an optional dependency
of this chart rather than a hard one.

The template lives here, not in charts/apps/<svc>/, because every service
renders from this library. What differs per service is the query and the
threshold, and those are values.
*/}}
{{- define "platform-service.analysistemplate" -}}
{{- range $name, $svc := .Values.services }}
{{- if and $svc.rollout $svc.rollout.analysis }}
{{- $an := $svc.rollout.analysis }}
---
apiVersion: argoproj.io/v1alpha1
kind: AnalysisTemplate
metadata:
  name: {{ $an.templateName | default (printf "%s-success-rate" $name) }}
  namespace: {{ include "platform-service.namespace" $ }}
  labels:
    {{- include "platform-service.labels" (dict "svc" (set $svc "name" $name) "root" $) | nindent 4 }}
spec:
  metrics:
    {{- range $m := $an.metrics }}
    - name: {{ $m.name }}
      {{/*
        interval must be longer than the gap between samples arriving, or the
        rate() window is evaluated over data that has not been pushed yet and
        every measurement reads the same stale point. Metrics reach Prometheus
        by push (service -> OTLP every 10s -> Alloy -> remote_write), not by
        the 15s scrape, so the arrival rate is what matters here.
      */}}
      interval: {{ $m.interval }}
      count: {{ $m.count }}
      {{/*
        initialDelay covers the gap between a canary pod going ready and its
        first metric export landing. Without it the first measurement reads a
        window in which the new pod had not yet reported, and judges the
        release on the old pods' data.
      */}}
      {{- with $m.initialDelay }}
      initialDelay: {{ . }}
      {{- end }}
      successCondition: {{ $m.successCondition | quote }}
      {{/*
        failureLimit counts measurements that failed the successCondition.
        Above 0 it tolerates a single bad window from one slow scrape rather
        than aborting a healthy release on noise.
      */}}
      failureLimit: {{ $m.failureLimit | default 0 }}
      {{/*
        consecutiveErrorLimit counts provider errors — Prometheus unreachable,
        or an expression that blew up. Argo's default is 4, which at this
        interval is minutes of a broken analysis before anyone finds out.
      */}}
      consecutiveErrorLimit: {{ $m.consecutiveErrorLimit | default 2 }}
      provider:
        prometheus:
          address: {{ $an.address }}
          query: |
            {{- $m.query | nindent 12 }}
    {{- end }}
{{- end }}
{{- end }}
{{- end -}}
