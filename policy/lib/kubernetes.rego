# Shared accessors. Every rule file imports this instead of reaching into the
# document itself, because the pod spec sits at a different depth per kind and
# a rule that only knows Deployment silently passes a CronJob that violates it.
package lib.kubernetes

import rego.v1

workload_kinds := {
	"Deployment",
	"StatefulSet",
	"DaemonSet",
	"Job",
	"CronJob",
	"ReplicaSet",
	"Pod",
	"Rollout",
}

is_workload if workload_kinds[input.kind]

# CronJob buries the pod two templates down; Pod has no template at all.
pod_spec := input.spec.jobTemplate.spec.template.spec if input.kind == "CronJob"

pod_spec := input.spec if input.kind == "Pod"

pod_spec := input.spec.template.spec if {
	input.kind != "CronJob"
	input.kind != "Pod"
	is_workload
}

# initContainers run as root far more often than app containers do, so a rule
# that only walks .containers leaves the most privileged process unchecked.
containers contains c if some c in pod_spec.containers

containers contains c if some c in pod_spec.initContainers

name := sprintf("%s/%s", [input.kind, input.metadata.name])
