{{/*
Renders only the shared ConfigMap. No workload, no Service — this release
exists to own one object that has no service of its own.
*/}}
{{ include "platform-service.configmap" . }}
