# `latest` is not a version. Two nodes pulling it a week apart run different
# code, and a rollback to the previous release rolls back to the same tag.
package main

import data.lib.kubernetes
import rego.v1

# What CI actually produces: a 7-40 char git sha, a digest pin, or a semver
# release. Anything else is a human typing a name.
tag_allowed(tag) if regex.match(`^[0-9a-f]{7,40}$`, tag)

tag_allowed(tag) if regex.match(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`, tag)

split_tag(image) := tag if {
	not contains(image, "@sha256:")
	parts := split(image, ":")
	count(parts) > 1
	tag := parts[count(parts) - 1]
}

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	tag := split_tag(c.image)
	tag == "latest"
	f := {
		"id": "image-tag",
		"severity": "deny",
		"msg": sprintf("%s: container %q pins image:latest", [kubernetes.name, c.name]),
	}
}

findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	tag := split_tag(c.image)
	tag != "latest"
	not tag_allowed(tag)
	f := {
		"id": "image-tag",
		"severity": "deny",
		"msg": sprintf("%s: container %q tag %q is not a git sha or semver", [kubernetes.name, c.name, tag]),
	}
}

# No colon at all means the runtime appends :latest for you — the same failure
# with nothing in the YAML to grep for.
findings contains f if {
	kubernetes.is_workload
	some c in kubernetes.containers
	not contains(c.image, ":")
	f := {
		"id": "image-tag",
		"severity": "deny",
		"msg": sprintf("%s: container %q has an untagged image %q", [kubernetes.name, c.name, c.image]),
	}
}
