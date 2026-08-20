package main

import rego.v1

with_image(img) := {
	"kind": "Deployment",
	"metadata": {"name": "auth"},
	"spec": {"template": {"spec": {"containers": [{
		"name": "auth",
		"image": img,
		"resources": {
			"requests": {"cpu": "50m", "memory": "96Mi"},
			"limits": {"cpu": "500m", "memory": "384Mi"},
		},
		"securityContext": {"runAsNonRoot": true},
	}]}}},
}

test_git_sha_allowed if {
	count(deny) == 0 with input as with_image("docker.io/nginnu/rabbit-auth:a1b2c3d")
}

test_semver_allowed if {
	count(deny) == 0 with input as with_image("docker.io/nginnu/rabbit-auth:v0.1.0")
}

test_digest_allowed if {
	count(deny) == 0 with input as with_image("docker.io/nginnu/rabbit-auth@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

test_latest_denied if {
	count(deny) == 1 with input as with_image("docker.io/nginnu/rabbit-auth:latest")
}

test_untagged_denied if {
	count(deny) == 1 with input as with_image("docker.io/nginnu/rabbit-auth")
}

test_named_tag_denied if {
	count(deny) == 1 with input as with_image("docker.io/nginnu/rabbit-auth:staging")
}

# A registry with a port has a colon that is not the tag separator. Splitting on
# the first colon would read "5000/rabbit-auth:v0.1.0" as the tag and deny a
# perfectly good pin.
test_registry_port_not_mistaken_for_tag if {
	count(deny) == 0 with input as with_image("registry.local:5000/rabbit-auth:v0.1.0")
}
