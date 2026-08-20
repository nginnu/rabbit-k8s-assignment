package main

import rego.v1

base_container := {
	"name": "auth",
	"image": "docker.io/nginnu/rabbit-auth:a1b2c3d",
	"resources": {
		"requests": {"cpu": "50m", "memory": "96Mi"},
		"limits": {"cpu": "500m", "memory": "384Mi"},
	},
}

wrap(pod_spec) := {"kind": "Deployment", "metadata": {"name": "auth"}, "spec": {"template": {"spec": pod_spec}}}

test_non_root_on_container_passes if {
	c := object.union(base_container, {"securityContext": {"runAsNonRoot": true}})
	count(deny) == 0 with input as wrap({"containers": [c]})
}

test_non_root_inherited_from_pod if {
	pod := {"containers": [base_container], "securityContext": {"runAsNonRoot": true}}
	count(deny) == 0 with input as wrap(pod)
}

test_unset_runasnonroot_denied if {
	count(deny) == 1 with input as wrap({"containers": [base_container]})
}

# The case a `not securityContext.runAsNonRoot` rule gets wrong: the pod opts in,
# the container opts back out, and the container is what the kubelet honours.
test_container_false_overrides_pod_true if {
	c := object.union(base_container, {"securityContext": {"runAsNonRoot": false}})
	pod := {"containers": [c], "securityContext": {"runAsNonRoot": true}}
	count(deny) == 1 with input as wrap(pod)
}

test_privileged_denied if {
	c := object.union(base_container, {"securityContext": {"runAsNonRoot": true, "privileged": true}})
	count(deny) == 1 with input as wrap({"containers": [c]})
}

test_host_network_denied if {
	c := object.union(base_container, {"securityContext": {"runAsNonRoot": true}})
	count(deny) == 1 with input as wrap({"containers": [c], "hostNetwork": true})
}

test_privilege_escalation_denied if {
	c := object.union(base_container, {"securityContext": {"runAsNonRoot": true, "allowPrivilegeEscalation": true}})
	count(deny) == 1 with input as wrap({"containers": [c]})
}
