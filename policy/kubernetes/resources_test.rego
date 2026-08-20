package main

import rego.v1

good_container := {
	"name": "auth",
	"image": "docker.io/nginnu/rabbit-auth:a1b2c3d",
	"resources": {
		"requests": {"cpu": "50m", "memory": "96Mi"},
		"limits": {"cpu": "500m", "memory": "384Mi"},
	},
	"securityContext": {"runAsNonRoot": true},
}

deployment(containers) := {
	"kind": "Deployment",
	"metadata": {"name": "auth"},
	"spec": {"template": {"spec": {"containers": containers}}},
}

test_compliant_container_passes if {
	count(deny) == 0 with input as deployment([good_container])
}

test_missing_requests_denied if {
	c := json.remove(good_container, ["resources/requests"])
	count(deny) > 0 with input as deployment([c])
}

test_missing_memory_limit_denied if {
	c := json.remove(good_container, ["resources/limits/memory"])
	count(deny) > 0 with input as deployment([c])
}

test_missing_cpu_limit_only_warns if {
	c := json.remove(good_container, ["resources/limits/cpu"])
	count(deny) == 0 with input as deployment([c])
	count(warn) == 1 with input as deployment([c])
}

# An initContainer with no limits can exhaust the node before the app container
# it is preparing ever starts, so it has to be walked too.
test_init_container_is_checked if {
	bad := {"name": "migrate", "image": "docker.io/nginnu/rabbit-auth:a1b2c3d", "securityContext": {"runAsNonRoot": true}}
	obj := {
		"kind": "Deployment",
		"metadata": {"name": "auth"},
		"spec": {"template": {"spec": {"containers": [good_container], "initContainers": [bad]}}},
	}
	count(deny) > 0 with input as obj
}

# A ConfigMap has no containers; a rule that fires on it is a rule that fires
# on everything.
test_non_workload_ignored if {
	count(deny) == 0 with input as {"kind": "ConfigMap", "metadata": {"name": "app-config"}}
}
