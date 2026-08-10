{{/*
Shared label set for every resource a service produces.

Called from within a `range` over services, where the dot context is the
per-service values map. We pass that map in as `.svc` (with `.name` set by
the caller) and the top-level chart context as `.root` so this helper can
still read `.Release.Service` for the managed-by label.

Expected argument: (dict "svc" $svc "root" $)
*/}}
{{- define "platform-service.labels" -}}
app.kubernetes.io/name: {{ .svc.name }}
app.kubernetes.io/instance: {{ .svc.name }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/part-of: platform
{{- end -}}

{{/*
Pod label selector — must match the labels above.
*/}}
{{- define "platform-service.selectorLabels" -}}
app.kubernetes.io/name: {{ .svc.name }}
{{- end -}}

{{/*
The full image reference: repository:tag. `svc` carries the image block.
*/}}
{{- define "platform-service.image" -}}
{{ .svc.image.repository }}:{{ .svc.image.tag | default .root.Chart.AppVersion }}
{{- end -}}

{{/*
Hardened securityContext.

Two fields vary per service; both default strict, so opting out is deliberate:

  uid                      numeric — kubelet cannot resolve a name
  readOnlyRootFilesystem   false only where the runtime writes to its own image
*/}}
{{- define "platform-service.securityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: {{ if hasKey .svc "readOnlyRootFilesystem" }}{{ .svc.readOnlyRootFilesystem }}{{ else }}true{{ end }}
runAsNonRoot: true
runAsUser: {{ .svc.uid | default 10001 }}
capabilities:
  drop: ["ALL"]
{{- end -}}
