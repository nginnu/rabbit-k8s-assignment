# The only place deny and warn are produced. Rule files state findings; this
# file decides what a finding costs.
package main

import rego.v1

deny contains msg if {
	some f in findings
	f.severity == "deny"
	not exempt(f.id)
	msg := f.msg
}

warn contains msg if {
	some f in findings
	f.severity == "warn"
	msg := f.msg
}

# An exempted finding is still printed, with the exemption named, so the count
# of things this cluster has agreed to live with stays visible on every run.
warn contains msg if {
	some f in findings
	f.severity == "deny"
	exempt(f.id)
	msg := sprintf("exempt[%s] %s", [f.id, f.msg])
}
