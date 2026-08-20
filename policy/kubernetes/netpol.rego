# Runs under `conftest --combine`: one input holding every document in the scan,
# because "this namespace has a default-deny" cannot be answered by looking at
# the namespace on its own — the answer is in a different file.
package netpol

import rego.v1

docs contains d if {
	some f in input
	d := f.contents
}

# The label the namespace manifests already carry. Using it rather than a
# hardcoded list means a namespace added later is covered the day it is added,
# not the day someone remembers to edit this file.
app_namespaces contains ns if {
	some d in docs
	d.kind == "Namespace"
	d.metadata.labels["app.kubernetes.io/part-of"] == "platform"
	ns := d.metadata.name
}

# `podSelector: {}` alone does not make a policy a default-deny — allow-dns is
# written exactly that way and opens port 53 for every pod in the namespace. The
# rule list has to be empty as well, which is what turns the policy from "these
# peers are permitted" into "none are".
is_default_deny(d, direction) if {
	d.kind == "NetworkPolicy"
	d.spec.podSelector == {}
	direction in d.spec.policyTypes
	object.get(d.spec, lower(direction), []) == []
}

default_deny_ingress contains ns if {
	some d in docs
	is_default_deny(d, "Ingress")
	ns := d.metadata.namespace
}

default_deny_egress contains ns if {
	some d in docs
	is_default_deny(d, "Egress")
	ns := d.metadata.namespace
}

deny contains msg if {
	some ns in app_namespaces
	not default_deny_ingress[ns]
	msg := sprintf("namespace %q has no default-deny ingress NetworkPolicy", [ns])
}

deny contains msg if {
	some ns in app_namespaces
	not default_deny_egress[ns]
	msg := sprintf("namespace %q has no default-deny egress NetworkPolicy", [ns])
}
