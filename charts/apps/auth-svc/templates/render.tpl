{{/*
Renders this service from the platform-service library chart.

A library chart never renders its own templates — the consumer must include
them. This file is that call, and it is deliberately identical in every
service chart: the differences between services belong in values.yaml, never
here. If this file ever needs to differ per service, the library is missing a
knob and that is where the fix goes.

The shared ConfigMap is NOT rendered here. It is one object used by every
service and belongs to the platform-config release, which syncs ahead of these
(wave 10 vs 20). Rendering it from each service would have six releases
fighting over the same object on every sync.
*/}}
{{ include "platform-service.deployment-rollout" . }}
{{ include "platform-service.service" . }}
{{ include "platform-service.pdb" . }}
{{ include "platform-service.networkpolicy" . }}
{{ include "platform-service.analysistemplate" . }}
