package main

import rego.v1

# Same violation, two workloads: one named in exemptions.rego, one not. The pair
# is the point — an exemption that silences the rule everywhere is not an
# exemption, it is a deleted rule.
statefulset(kind, name, image) := {
	"kind": kind,
	"metadata": {"name": name},
	"spec": {"template": {"spec": {"containers": [{
		"name": name,
		"image": image,
		"resources": {
			"requests": {"cpu": "50m", "memory": "96Mi"},
			"limits": {"cpu": "500m", "memory": "384Mi"},
		},
		"securityContext": {"runAsNonRoot": true},
	}]}}},
}

test_exempt_workload_warns_instead_of_denying if {
	obj := statefulset("StatefulSet", "mariadb", "mariadb:11.4")
	count(deny) == 0 with input as obj
	count(warn) == 1 with input as obj
}

test_exempt_finding_is_still_printed if {
	obj := statefulset("StatefulSet", "mariadb", "mariadb:11.4")
	some msg in warn with input as obj
	startswith(msg, "exempt[image-tag]") with input as obj
}

test_same_violation_elsewhere_still_denies if {
	obj := statefulset("Deployment", "auth", "docker.io/nginnu/rabbit-auth:11.4")
	count(deny) == 1 with input as obj
}

# The exemption is per rule id, not per workload: mariadb is excused its tag and
# its root user, nothing else.
test_exemption_does_not_cover_other_rules if {
	obj := {
		"kind": "StatefulSet",
		"metadata": {"name": "mariadb"},
		"spec": {"template": {"spec": {
			"hostNetwork": true,
			"containers": [{
				"name": "mariadb",
				"image": "mariadb:11.4",
				"resources": {"requests": {"cpu": "50m", "memory": "96Mi"}, "limits": {"cpu": "1", "memory": "1Gi"}},
				"securityContext": {"runAsNonRoot": true},
			}],
		}}},
	}
	count(deny) == 1 with input as obj
}
