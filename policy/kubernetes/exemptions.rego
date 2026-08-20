# Every exemption, in one file, keyed by the workload it applies to.
#
# An exemption does not silence a finding — gate.rego turns it into a warning
# that still prints on every run and still counts. The alternative in practice
# is a gate that is red on the day it is switched on, which gets switched off
# the same week.
#
# Adding an entry here is a review: it names the workload, the rule, and why.
package main

import rego.v1

exemptions := {
	# Upstream images published by their vendors, not built here. mariadb:11.4
	# and redis:7.4-alpine are the tags those projects ship; rewriting them to a
	# sha would pin us to a digest we do not control and cannot rebuild.
	"StatefulSet/mariadb": {"image-tag", "run-as-non-root"},
	"Job/mariadb-init": {"image-tag", "run-as-non-root"},
	"Deployment/redis": {"image-tag"},
}

# mariadb 11.4 needs a writable datadir owned by uid 999 and refuses to start
# under runAsNonRoot without a fsGroup change to the volume — a StatefulSet
# migration, not a YAML edit. Tracked, not forgotten.
exempt(id) if {
	ids := exemptions[data.lib.kubernetes.name]
	id in ids
}
