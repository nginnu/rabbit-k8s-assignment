# A container with no requests is scheduled as best-effort: the scheduler packs
# it anywhere and the kubelet evicts it first under memory pressure. A container
# with no limits can take the node down with it.
package main

import data.lib.kubernetes
import rego.v1

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	not c.resources.requests.cpu
	f := {
		"id": "resource-requests",
		"severity": "deny",
		"msg": sprintf("%s: container %q has no resources.requests.cpu", [kubernetes.name, c.name]),
	}
}

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	not c.resources.requests.memory
	f := {
		"id": "resource-requests",
		"severity": "deny",
		"msg": sprintf("%s: container %q has no resources.requests.memory", [kubernetes.name, c.name]),
	}
}

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	not c.resources.limits.memory
	f := {
		"id": "resource-limits",
		"severity": "deny",
		"msg": sprintf("%s: container %q has no resources.limits.memory", [kubernetes.name, c.name]),
	}
}

# CPU limits are deliberately a warning, not a denial. Throttling a service that
# is merely bursty costs latency for no safety gain, so the call belongs to
# whoever owns the service — but it should not be made by forgetting.
findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	not c.resources.limits.cpu
	f := {
		"id": "cpu-limit",
		"severity": "warn",
		"msg": sprintf("%s: container %q has no resources.limits.cpu", [kubernetes.name, c.name]),
	}
}
