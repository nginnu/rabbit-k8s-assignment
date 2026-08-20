package netpol

import rego.v1

ns(name) := {"kind": "Namespace", "metadata": {"name": name, "labels": {"app.kubernetes.io/part-of": "platform"}}}

# Not named deny_* on purpose: conftest picks the rules it evaluates by the
# prefixes deny/warn/violation, so a test helper called deny_policy is queried
# as a policy rule and every run aborts with a type error before any file is
# checked.
blanket(name, ns_name, types) := {
	"kind": "NetworkPolicy",
	"metadata": {"name": name, "namespace": ns_name},
	"spec": {"podSelector": {}, "policyTypes": types},
}

files(docs) := [f | some d in docs; f := {"path": "x.yaml", "contents": d}]

both_defaults(n) := [blanket("deny-in", n, ["Ingress"]), blanket("deny-eg", n, ["Egress"])]

test_namespace_with_both_defaults_passes if {
	count(deny) == 0 with input as files(array.concat([ns("api")], both_defaults("api")))
}

test_namespace_without_any_policy_denied if {
	count(deny) == 2 with input as files([ns("api")])
}

test_ingress_only_still_denies_egress if {
	docs := [ns("api"), blanket("deny-in", "api", ["Ingress"])]
	count(deny) == 1 with input as files(docs)
}

# The false green this rule shipped with: allow-dns selects every pod and lists
# Egress in policyTypes, so a check on podSelector alone reads it as the
# default-deny and the namespace passes with nothing denying anything.
test_allow_policy_is_not_a_default_deny if {
	allow_dns := {
		"kind": "NetworkPolicy",
		"metadata": {"name": "allow-dns", "namespace": "api"},
		"spec": {
			"podSelector": {},
			"policyTypes": ["Egress"],
			"egress": [{"ports": [{"port": 53, "protocol": "UDP"}]}],
		},
	}
	docs := [ns("api"), blanket("deny-in", "api", ["Ingress"]), allow_dns]
	count(deny) == 1 with input as files(docs)
}

# A policy that names pods is not a default-deny either: anything it does not
# select stays wide open.
test_scoped_policy_does_not_count_as_default_deny if {
	scoped := {
		"kind": "NetworkPolicy",
		"metadata": {"name": "auth", "namespace": "api"},
		"spec": {"podSelector": {"matchLabels": {"app": "auth"}}, "policyTypes": ["Ingress", "Egress"]},
	}
	count(deny) == 2 with input as files([ns("api"), scoped])
}

# Infrastructure namespaces are not labelled part-of: platform and are governed
# by their upstream charts, so the rule must not reach into them.
test_unlabelled_namespace_ignored if {
	plain := {"kind": "Namespace", "metadata": {"name": "traefik"}}
	count(deny) == 0 with input as files([plain])
}
