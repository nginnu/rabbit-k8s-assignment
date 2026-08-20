{{/*
Renders this service from the platform-service library chart.

A library chart never renders its own templates — the consumer must include
them. This file is that call, and it is meant to be identical in every service
chart: the differences between services belong in values.yaml, never here. If
this file ever needs to differ per service, the library is missing a knob and
that is where the fix goes.
*/}}
{{ include "platform-service.deployment-rollout" . }}
{{ include "platform-service.service" . }}
{{ include "platform-service.httproute" . }}
{{ include "platform-service.pdb" . }}
{{ include "platform-service.networkpolicy" . }}
