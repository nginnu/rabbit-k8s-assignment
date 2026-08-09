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

Two fields vary and both default to the strict value, so a service opts out
deliberately rather than inheriting a weaker setting by accident:

  uid                      the image's own user; kubelet needs the number
  readOnlyRootFilesystem   false only where the runtime genuinely writes into
                           its own image at run time — a cache directory, for
                           instance. Anything that does not, stays read-only.

The remaining three are not negotiable and take no value from the caller.
*/}}
{{- define "platform-service.securityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: {{ if hasKey .svc "readOnlyRootFilesystem" }}{{ .svc.readOnlyRootFilesystem }}{{ else }}true{{ end }}
runAsNonRoot: true
runAsUser: {{ .svc.uid | default 10001 }}
capabilities:
  drop: ["ALL"]
{{- end -}}
