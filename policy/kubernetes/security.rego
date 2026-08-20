# Three ways a pod stops being isolated from the node it lands on.
package main

import data.lib.kubernetes
import rego.v1

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	c.securityContext.privileged == true
	f := {
		"id": "privileged",
		"severity": "deny",
		"msg": sprintf("%s: container %q runs privileged", [kubernetes.name, c.name]),
	}
}

# hostNetwork puts the pod on the node's network namespace: it can reach every
# NodePort and every host-local service, and no NetworkPolicy applies to it.
findings contains f if {
	kubernetes.is_workload
	kubernetes.pod_spec.hostNetwork == true
	f := {
		"id": "host-namespace",
		"severity": "deny",
		"msg": sprintf("%s: hostNetwork is true", [kubernetes.name]),
	}
}

findings contains f if {
	kubernetes.is_workload
	kubernetes.pod_spec.hostPID == true
	f := {
		"id": "host-namespace",
		"severity": "deny",
		"msg": sprintf("%s: hostPID is true", [kubernetes.name]),
	}
}

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	c.securityContext.allowPrivilegeEscalation == true
	f := {
		"id": "privilege-escalation",
		"severity": "deny",
		"msg": sprintf("%s: container %q allows privilege escalation", [kubernetes.name, c.name]),
	}
}

# runAsNonRoot is read from the container first and the pod second, because a
# container that sets it to false overrides a pod that sets it to true. A rule
# written with `not ...runAsNonRoot` cannot tell that false apart from unset and
# passes the container that explicitly opted back into root.
container_value(c) := object.get(c, ["securityContext", "runAsNonRoot"], null)

pod_value := object.get(kubernetes.pod_spec, ["securityContext", "runAsNonRoot"], null)

effective(c) := v if {
	v := container_value(c)
	v != null
}

effective(c) := pod_value if container_value(c) == null

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	effective(c) != true
	f := {
		"id": "run-as-non-root",
		"severity": "deny",
		"msg": sprintf("%s: container %q does not set runAsNonRoot: true", [kubernetes.name, c.name]),
	}
}
