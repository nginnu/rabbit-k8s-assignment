# Local platform — one command per thing you actually want to do.
#
# kind and helm live in /usr/local/bin, which a login shell does not always have
# on PATH.
export PATH := /usr/local/bin:$(PATH)

CLUSTER   ?= rabbit-k8s-test
MANIFESTS := platform/manifests
VERSION   ?= v0.1.0

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F: '{printf "  \033[36m%-16s\033[0m%s\n", $$1, $$2}'

## cluster: create the kind cluster
cluster:
	@# No --wait. The cluster config disables the default CNI, so no node can
	@# reach Ready until cilium: runs — kind would sit out the whole timeout and
	@# then print "WARNING: Timed out waiting for Ready" on every fresh create,
	@# which reads like a broken cluster and is not one. Node readiness is waited
	@# on in cilium:, by the target that actually delivers it.
	@kind get clusters | grep -qx $(CLUSTER) \
		&& echo "cluster $(CLUSTER) already exists" \
		|| kind create cluster --name $(CLUSTER) \
			--config platform/kind/cluster.yaml

# Chart and agent image ship in lockstep — chart 1.20.0 carries appVersion
# 1.20.0 — so unlike traefik there is one pin here, not two, and the drift check
# below only adds the v the image tag prefixes.
CILIUM_VERSION := 1.20.0

## cilium: install the CNI — no pod schedules anywhere until this runs
# A target wired to cluster: instead of cilium: skips the CNI, and its first pod
# sits Pending with no event that names the missing network as the reason.
cilium: cluster
	@# oci:// rather than a repo add/update pair: there is no local repo cache to
	@# go stale and resolve a chart older than the pin, which is the drift the
	@# argocd check further down exists to catch.
	@# No --set: ipam mode and the kind-specific bits belong in the values file,
	@# and a flag here would override it silently.
	@# No --create-namespace: kubeadm already made kube-system, and the flag
	@# would turn a typo in -n into a new empty namespace instead of an error.
	@# 10m, not the 5m used elsewhere: this is the one chart whose image is
	@# pulled onto four nodes at once before anything can be ready.
	@helm upgrade --install cilium oci://quay.io/cilium/charts/cilium \
		-n kube-system --version $(CILIUM_VERSION) \
		-f platform/addons/cilium/local/values.yaml \
		--wait --timeout 10m
	@# The rendered image carries a digest as well as a tag
	@# (cilium:v1.20.0@sha256:...), so the tag has to be cut out from between
	@# them: stripping to the last colon returns the sha256 hex and the check
	@# fails against a version that was never wrong.
	@./platform/scripts/check-version.sh cilium v$(CILIUM_VERSION) \
		"$$(kubectl -n kube-system get daemonset cilium \
			-o jsonpath='{.spec.template.spec.containers[?(@.name=="cilium-agent")].image}' \
			| sed 's/@.*//; s/.*://')"
	@# helm --wait proves the DaemonSet is ready, not that the kubelets picked
	@# the CNI config up. Until every node is Ready the next target's pods stay
	@# Pending, and the scheduler says only "0/4 nodes are available".
	@kubectl wait --for=condition=Ready nodes --all --timeout=180s

ISTIO_VERSION := 1.30.3
KIALI_VERSION := 2.30.0

## istio: install the mesh control plane — it injects nothing until a namespace asks
# Deliberately not on gateway:, and no istio gateway chart is installed: a second
# Gateway controller writes its own status onto the same HTTPRoutes and the
# request reaches whichever one owns the hostPort.
istio: cilium
	@# Not oci://: Istio's ghcr.io mirror denies anonymous pulls (403 on the token
	@# request), so an oci:// ref fails the install outright on a machine with no
	@# ghcr credentials.
	@helm repo add istio https://istio-release.storage.googleapis.com/charts >/dev/null 2>&1 || true
	@helm repo update istio >/dev/null
	@# base carries the CRDs. istiod's templates reference them at render time,
	@# so installing istiod first fails on kinds the API server never heard of.
	@helm upgrade --install istio-base istio/base \
		-n istio-system --create-namespace --version $(ISTIO_VERSION) \
		--wait --timeout 5m
	@# No --set: a flag here overrides the values file silently and the file stops
	@# describing what runs.
	@helm upgrade --install istiod istio/istiod \
		-n istio-system --version $(ISTIO_VERSION) \
		-f platform/addons/istio/local/values.yaml \
		--wait --timeout 5m
	@# No istio-cni: cilium runs cni-exclusive=true and deletes the plugin config
	@# istio-cni chains onto the node's conflist, so pods start with no redirect,
	@# stay Ready, and carry no sidecar traffic with nothing logging why. istio-init
	@# does the same work inside the pod.
	@./platform/scripts/check-version.sh istiod $(ISTIO_VERSION) \
		"$$(kubectl -n istio-system get deploy istiod \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"
	@# By file, not by folder: a future PeerAuthentication STRICT lands in the same
	@# folder and must never ride in on a directory apply — it cuts every edge route
	@# the moment Traefik, still unmeshed, keeps sending plaintext (notes/09).
	@kubectl apply -f $(MANIFESTS)/addons/istio/destination-rule.yaml

## kiali: mesh console — the graph of who talks to whom, over the running mesh
# After observability as well as istio: Kiali reads Istio CRs from the API server
# and every number it shows from Prometheus, so an install ahead of either comes
# up as a console with a blank graph, which reads as broken rather than early.
kiali: istio observability
	@helm repo add kiali https://kiali.org/helm-charts >/dev/null 2>&1 || true
	@helm repo update kiali >/dev/null
	@# In observability, not istio-system: the NetworkPolicy that opens the path to
	@# Prometheus is the one on this namespace.
	@helm upgrade --install kiali kiali/kiali-server \
		-n observability --version $(KIALI_VERSION) \
		-f platform/addons/kiali/local/values.yaml \
		--wait --timeout 5m
	@./platform/scripts/check-version.sh kiali $(KIALI_VERSION) \
		"$$(kubectl -n observability get deploy kiali \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://; s/^v//')"
	@# The host the route answers on has to be in the certificate or the browser
	@# turns the console into a TLS warning that reads as a broken install. The
	@# wait blocks on a fresh cluster, where no Secret exists yet; on a re-run it
	@# can return on the previous Ready and the reissue lands a moment later.
	@kubectl apply -f $(MANIFESTS)/addons/traefik/certificate.yaml
	@kubectl -n traefik wait --for=condition=Ready certificate/platform-tls --timeout=120s
	@kubectl apply -f $(MANIFESTS)/addons/kiali/route.yaml

## namespaces: create the namespaces everything else lands in
# On cilium:, not cluster:. data, observability, rollouts, argocd and apps reach
# the cluster through here and nowhere else, and without the edge `make apps`
# hangs in rollout status with nothing naming the network.
namespaces: cilium
	@# Every namespace folder sits at depth 2 — apps/, addons/, local/ — so one
	@# glob reaches all of them; a folder created one level off is not matched and
	@# is skipped in silence, and the first target to apply into it fails on a
	@# namespace that was never created. The loop stays because kubectl takes a
	@# single path per -f: a bare `-f <glob>` hands it the first match and drops
	@# the rest as positional junk.
	@for f in $(MANIFESTS)/*/*/namespace.yaml; do \
		kubectl apply -f $$f; \
	done

GATEWAY_API_VERSION := v1.6.1

## gateway-api: install Gateway API CRDs — must run before Traefik
# On cilium:, not cluster:. traefik -> tls -> gateway hangs off this edge, and
# all three schedule pods and then block on --wait.
gateway-api: cilium
	@kubectl apply --server-side --force-conflicts -f \
		https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml
	@kubectl wait --for=condition=established --timeout=60s \
		crd/gateways.gateway.networking.k8s.io \
		crd/httproutes.gateway.networking.k8s.io \
		crd/gatewayclasses.gateway.networking.k8s.io
	@# apply does not replace CRDs from an older bundle, and the listener fields
	@# the Gateway needs are then dropped at admission while the YAML still looks
	@# right. Traefik documents support for this bundle version.
	@./platform/scripts/check-version.sh gateway-api $(GATEWAY_API_VERSION) \
		"$$(kubectl get crd gateways.gateway.networking.k8s.io \
			-o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}')"

TRAEFIK_NS := traefik

# Two different strings, and both are load-bearing. TRAEFIK_VERSION is the chart
# and is what --version pins; TRAEFIK_APP_VERSION is the proxy image tag, and it
# is the only one a running cluster reports back, so the drift check below
# compares that. Bumping one without the other reinstalls the same proxy from a
# chart that renders different fields, or the reverse.
TRAEFIK_VERSION     := 41.2.0
TRAEFIK_APP_VERSION := v3.7.10

## traefik: install the controller that owns the Gateway
traefik: gateway-api
	@helm repo add traefik https://traefik.github.io/charts >/dev/null 2>&1 || true
	@helm repo update traefik >/dev/null
	@# No --set: providers, listeners and hostPorts all live in the values file.
	@# A flag here would override it silently and the file would stop describing
	@# what is actually running.
	@helm upgrade --install traefik traefik/traefik -n $(TRAEFIK_NS) --create-namespace \
		--version $(TRAEFIK_VERSION) -f platform/addons/traefik/local/values.yaml \
		--wait --timeout 5m
	@# A Traefik below the pin ignores listener fields it does not know and logs
	@# nothing: the Gateway still reads correctly in git while :443 never binds,
	@# and the failure surfaces as a connection refused several targets later.
	@./platform/scripts/check-version.sh traefik $(TRAEFIK_APP_VERSION) \
		"$$(kubectl -n $(TRAEFIK_NS) get deploy traefik \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

CERT_MANAGER_VERSION := v1.21.1

## tls: install cert-manager and load the local CA it issues from
# After traefik, not before: the Gateway reads its certificate from a Secret in
# its own namespace, and that namespace is created by the Traefik release.
tls: traefik
	@helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
	@helm repo update jetstack >/dev/null
	@helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager \
		--create-namespace --version $(CERT_MANAGER_VERSION) \
		--set crds.enabled=true --wait --timeout 5m
	@# The CA has to come from mkcert on this machine: a browser only trusts a CA
	@# already in the system keychain, and nothing in the cluster can add one.
	@./platform/scripts/seed-ca.sh
	@# Issuer before certificate: a Certificate naming an issuer that does not
	@# exist yet stays not-ready, and the wait below burns its whole 120s before
	@# anything says why.
	@kubectl apply -f $(MANIFESTS)/cluster/clusterissuer.yaml
	@kubectl apply -f $(MANIFESTS)/addons/traefik/certificate.yaml
	@kubectl -n $(TRAEFIK_NS) wait --for=condition=Ready --timeout=120s \
		certificate/platform-tls

## gateway: attach the routes to the Gateway
gateway: namespaces tls
	@# Nothing to apply first — the Gateway comes from the Traefik release. It is
	@# created before the certificate exists, so the wait for Programmed belongs
	@# here, after tls, not inside the traefik target.
	@kubectl -n $(TRAEFIK_NS) wait --for=condition=Programmed --timeout=180s gateway/platform
	@kubectl apply -f $(MANIFESTS)/apps/web -f $(MANIFESTS)/apps/api

## traefik-dashboard: expose the Traefik dashboard through the platform Gateway
# After gateway, not before. A route applied while the Gateway is not yet
# Programmed has no controller to attach it and sits orphaned with no error at
# the browser. And after traefik has re-run: the dashboard backend is
# traefik:8080, and until the release is upgraded with the values that expose
# that port the Service has no 8080, so the route attaches cleanly and answers
# 500. gateway pulls traefik in transitively (gateway -> tls -> traefik), so the
# dependency on gateway alone covers both.
traefik-dashboard: gateway
	@kubectl apply -f $(MANIFESTS)/addons/traefik/route.yaml

## hubble: expose the Hubble UI through the platform Gateway
# After gateway, same reason as traefik-dashboard: a route applied before the
# Gateway is Programmed has no controller to attach it and sits orphaned with
# no error at the browser.
hubble: gateway
	@kubectl apply -f $(MANIFESTS)/addons/cilium/route.yaml

DATA_NS := data

## secrets: generate credentials into the cluster, never into git
secrets: namespaces
	@# Created once: regenerating would change the password out from under a
	@# database that still has the old one.
	@kubectl -n $(DATA_NS) get secret mariadb >/dev/null 2>&1 \
		&& echo "secret mariadb already exists — delete it to rotate" \
		|| kubectl -n $(DATA_NS) create secret generic mariadb \
			--from-literal=username=rabbitshop \
			--from-literal=password=$$(openssl rand -hex 16) \
			--from-literal=root-password=$$(openssl rand -hex 16)

# The frontend and the services are separate namespaces, because a
# NetworkPolicy peer written as a bare podSelector only ever matches inside its
# own namespace — together, "the storefront may not reach MariaDB" cannot be
# said without naming pods. Both strings are also written in each chart's
# values.yaml, which is where the rendered objects take their namespace from;
# chart-namespace.sh reads them back out of there rather than mapping charts to
# namespaces a second time here.
WEB_NS := web
API_NS := api
APP_NS := $(WEB_NS) $(API_NS)

## app-secrets: build the app credentials from the database ones
app-secrets: secrets
	@# Only the Go services read this, and a Secret is namespaced: a second copy
	@# in $(WEB_NS) would be a database password and a JWT signing key sitting
	@# unread in the namespace that faces the browser.
	@DBPASS=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.password}' | base64 -d); \
	DBUSER=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.username}' | base64 -d); \
	kubectl -n $(API_NS) get secret app-secrets >/dev/null 2>&1 \
		|| kubectl -n $(API_NS) create secret generic app-secrets \
			--from-literal=mariadb-dsn="$$DBUSER:$$DBPASS@tcp(mariadb.$(DATA_NS).svc.cluster.local:3306)/rabbitshop?parseTime=true" \
			--from-literal=jwt-secret=$$(openssl rand -hex 32)

## sql: load the schema and seed into a ConfigMap
sql: namespaces
	@kubectl -n $(DATA_NS) create configmap mariadb-init-sql \
		--from-file=init.sql=storage/mariadb-init.sql \
		--dry-run=client -o yaml | kubectl apply -f -

## data: bring up MariaDB and Redis
data: secrets sql
	@# The policies go on with the database, never after it. Every service chart
	@# now sets networkPolicy: true, and a policy is one-sided: the egress the
	@# charts grant reaches nothing until the matching ingress in netpol.yaml
	@# exists. Left to a later target, every query out of $(API_NS) is dropped for
	@# the length of the gap and reads as a database outage rather than a missing
	@# manifest — applying the folder in one shot is what keeps them together.
	@kubectl apply -f $(MANIFESTS)/local/data
	@kubectl -n $(DATA_NS) rollout status statefulset/mariadb --timeout=180s
	@kubectl -n $(DATA_NS) rollout status deployment/redis --timeout=120s
	@# The schema Job runs separately from the database, so waiting on the
	@# StatefulSet alone would let a service start against empty tables.
	@kubectl -n $(DATA_NS) wait --for=condition=complete job/mariadb-init --timeout=180s

# Each name is both a cmd/ directory under apps/services (passed as the SVC
# build arg) and the image tag its chart pins. A service left off this list
# still passes check-image-tags.sh — that compares tags, not names — so nothing
# is built or loaded and the pod sits in ErrImageNeverPull.
GO_SVCS := auth catalog order-svc payment-svc payment

## images: build every image and load it into the cluster
images:
	@# pullPolicy is Never, so a chart pinned to an unbuilt tag does not fail at
	@# deploy time — the pod just sits in ErrImageNeverPull.
	@./platform/scripts/check-image-tags.sh $(VERSION)
	@docker build --build-arg VERSION=$(VERSION) -t notification:$(VERSION) apps/notification
	@for s in $(GO_SVCS); do \
		docker build --build-arg SVC=$$s --build-arg VERSION=$(VERSION) \
			-t $$s:$(VERSION) apps/services; \
	done
	@docker build -t web-ui:$(VERSION) apps/web-ui
	@# kind nodes have their own image store and no registry, so an image built on
	@# the host is invisible until loaded in.
	@kind load docker-image --name $(CLUSTER) \
		notification:$(VERSION) web-ui:$(VERSION) \
		$(foreach s,$(GO_SVCS),$(s):$(VERSION))

# One release per service, so one can be rolled back without its siblings.
# platform-config first: a pod that mounts a ConfigMap which does not exist yet
# stays Pending rather than failing loudly.
#
# Helm-owned only. A chart that has moved to Argo CD is deleted from these two
# lists in the same edit that adds it to GITOPS_CHARTS below — see there for
# what breaks when it stays in both.
APP_CHARTS := platform-config auth catalog order-svc payment-svc payment web-ui

# platform-config renders only a ConfigMap, so there is nothing to wait on.
APP_DEPLOYS := auth catalog order-svc payment-svc payment web-ui

# The charts that have graduated out of the helm loop above and belong to Argo
# CD now. The migration is one service at a time, so this list grows and
# APP_CHARTS shrinks by the same name; gitops-bootstrap loops it and needs no
# new lines when the next one moves.
#
# A name left in both lists breaks the second `make up` and every one after it.
# The Application syncs with ServerSideApply=true, which force-takes the fields
# from helm's field manager; a plain `helm upgrade` does not force back, so it
# dies on the fields Argo CD now owns:
#
#   Error: UPGRADE FAILED: conflict occurred while applying object api/notification
#   apps/v1, Kind=Deployment: Apply failed with 2 conflicts: conflicts with
#   "argocd-controller": .spec.template.spec.containers[name="notification"]
#   .livenessProbe.initialDelaySeconds
#
# The first run passes — helm installs before the Application exists — so the
# mistake surfaces one full run after it is made.
GITOPS_CHARTS := notification

## apps: deploy the application services from their charts
apps: data app-secrets
	@# -n comes from the chart's own values.yaml, read back by the script. helm
	@# keeps release metadata in the namespace of -n while the objects carry the
	@# namespace the chart rendered them with, so a -n that disagrees with the
	@# values file installs pods that run and a release `helm list -n $(API_NS)`
	@# cannot see and `helm uninstall` will not remove.
	@for c in $(APP_CHARTS); do \
		ns=$$(./platform/scripts/chart-namespace.sh $$c) || exit 1; \
		helm dependency update charts/apps/$$c >/dev/null; \
		helm upgrade --install $$c charts/apps/$$c -n $$ns; \
	done
	@for d in $(APP_DEPLOYS); do \
		ns=$$(./platform/scripts/chart-namespace.sh $$d) || exit 1; \
		kubectl -n $$ns rollout status deployment/$$d --timeout=180s; \
	done

# Upstream charts. We own the values files and nothing else.
#
# prometheus-community/prometheus rather than kube-prometheus-stack: the stack
# pulls Grafana in as a dependency, which collides with the grafana release that
# owns our datasources, and brings an Operator, CRDs, Alertmanager and exporters
# that nothing here reads. Our Prometheus receives remote-write and scrapes
# nothing.
PROMETHEUS_VERSION := 29.23.0
LOKI_VERSION       := 7.2.0
TEMPO_VERSION      := 1.24.4
ALLOY_VERSION      := 1.11.1
GRAFANA_VERSION    := 10.5.15

OBS_VALUES := platform/addons/observability/values

## observability: prometheus, loki, tempo, alloy and grafana
observability: namespaces
	@kubectl apply -f $(MANIFESTS)/addons/observability/collector-service.yaml
	@# Before the components, not after: applied last, a rule that selects the
	@# wrong label drops exports without failing anything — the OTel SDK logs the
	@# failure and keeps serving, so the only symptom is empty dashboards. Applied
	@# here, the same mistake shows up in the rollout waits at the end of this
	@# target. The namespace has to exist first, which the namespaces target
	@# creates — which is also why this target names files rather than the folder.
	@kubectl apply -f $(MANIFESTS)/addons/observability/netpol.yaml
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo add grafana https://grafana.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo update prometheus-community grafana >/dev/null
	@# The dashboard is a file rather than a values blob so it stays valid JSON
	@# that Grafana can export back into.
	@kubectl -n observability create configmap grafana-dashboards \
		--from-file=platform.json=platform/addons/observability/dashboard.json \
		--dry-run=client -o yaml | kubectl apply -f -
	@helm upgrade --install prometheus prometheus-community/prometheus \
		-n observability --version $(PROMETHEUS_VERSION) -f $(OBS_VALUES)/prometheus.yaml
	@helm upgrade --install loki grafana/loki \
		-n observability --version $(LOKI_VERSION) -f $(OBS_VALUES)/loki.yaml
	@helm upgrade --install tempo grafana/tempo \
		-n observability --version $(TEMPO_VERSION) -f $(OBS_VALUES)/tempo.yaml
	@helm upgrade --install alloy grafana/alloy \
		-n observability --version $(ALLOY_VERSION) -f $(OBS_VALUES)/alloy.yaml
	@helm upgrade --install grafana grafana/grafana \
		-n observability --version $(GRAFANA_VERSION) -f $(OBS_VALUES)/grafana.yaml
	@kubectl -n observability rollout status deployment/alloy --timeout=180s
	@kubectl -n observability rollout status deployment/grafana --timeout=180s
	@kubectl -n observability rollout status statefulset/loki --timeout=180s
	@kubectl apply -f $(MANIFESTS)/addons/observability/route.yaml

ROLLOUTS_VERSION     := 2.41.1
# The controller image tag, which is what a running cluster reports. The chart
# version is not recoverable from the Deployment.
ROLLOUTS_APP_VERSION := v1.9.1

## rollouts: install the Argo Rollouts controller
rollouts: namespaces
	@helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	@helm repo update argo >/dev/null
	@helm upgrade --install argo-rollouts argo/argo-rollouts \
		-n argo-rollouts --create-namespace --version $(ROLLOUTS_VERSION)
	@kubectl -n argo-rollouts rollout status deployment/argo-rollouts --timeout=180s
	@# Helm does not upgrade CRDs that already exist, so a cluster carrying an
	@# older bundle drops canary and analysis fields at admission and the Rollout
	@# still looks correct in git.
	@./platform/scripts/check-version.sh argo-rollouts $(ROLLOUTS_APP_VERSION) \
		"$$(kubectl -n argo-rollouts get deploy argo-rollouts \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

ARGOCD_VERSION     := 10.3.2
# The server image tag, which is what a running cluster reports. As with
# rollouts, the chart version is not recoverable from the Deployment.
ARGOCD_APP_VERSION := v3.5.0

## argocd: install the Argo CD control plane, served at https://argocd.localhost
argocd: namespaces
	@helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	@helm repo update argo >/dev/null
	@helm upgrade --install argocd argo/argo-cd \
		-n argocd --create-namespace --version $(ARGOCD_VERSION) \
		-f platform/addons/argocd/values/argocd.yaml
	@kubectl -n argocd rollout status deployment/argocd-server --timeout=300s
	@# Unlike rollouts, this chart keeps its CRDs in templates/ so an upgrade does
	@# update them. The check is here because the pin and the running server
	@# still drift when a repo cache is stale and helm resolves an older chart.
	@./platform/scripts/check-version.sh argocd $(ARGOCD_APP_VERSION) \
		"$$(kubectl -n argocd get deploy argocd-server \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"
	@# Named files, not the folder — the second folder here that cannot go on in
	@# one shot, for the same reason as addons/observability: one file in it
	@# belongs to a later moment than the rest. applications.yaml is that file,
	@# and gitops-bootstrap applies it after apps.
	@kubectl apply -f $(MANIFESTS)/addons/argocd/namespace.yaml \
		-f $(MANIFESTS)/addons/argocd/route.yaml

## gitops-bootstrap: hand the graduated charts to Argo CD, once each
# After apps, never before, because these Applications sync automatically.
# Applied first, Argo CD creates the Deployment, Service, NetworkPolicy and PDB
# itself, none of them carrying meta.helm.sh ownership, and the helm install
# below refuses to adopt them:
#
#   Error: unable to continue with install: NetworkPolicy "notification" in namespace
#   "api" exists and cannot be imported into the current release: invalid
#   ownership metadata; label validation error: missing key
#   "app.kubernetes.io/managed-by": must be set to "Helm"
#
# Only one direction works: helm creates the release, then ServerSideApply=true
# lets Argo CD take the objects over. No prerequisite on apps — up sequences the
# two already, and a prerequisite would re-run the whole helm loop in this
# sub-make just to apply one file.
gitops-bootstrap:
	@# Once, per chart, guarded the same way cluster: and secrets: are. The
	@# handoff is not repeatable: after the first sync the fields belong to
	@# argocd-controller, and a second `helm upgrade` over them fails on the
	@# conflict described at GITOPS_CHARTS. The Application existing is the
	@# record that it already happened — the helm release alone is not, since it
	@# survives the handoff and says nothing about who owns the objects now.
	@for c in $(GITOPS_CHARTS); do \
		if kubectl -n argocd get application $$c >/dev/null 2>&1; then \
			echo "application $$c already handed to Argo CD — helm handoff skipped"; \
			continue; \
		fi; \
		ns=$$(./platform/scripts/chart-namespace.sh $$c) || exit 1; \
		helm dependency update charts/apps/$$c >/dev/null; \
		helm upgrade --install $$c charts/apps/$$c -n $$ns; \
		kubectl -n $$ns rollout status deployment/$$c --timeout=180s; \
	done
	@kubectl apply -f $(MANIFESTS)/addons/argocd/applications.yaml
	@# A name in GITOPS_CHARTS that applications.yaml has no Application for
	@# would keep reinstalling through the guard above on every run and look
	@# exactly like a chart that graduated. Nothing else in the stack reports it,
	@# and the drift is only found when someone edits git and waits for a sync
	@# that never comes.
	@for c in $(GITOPS_CHARTS); do \
		kubectl -n argocd get application $$c >/dev/null 2>&1 || { \
			echo "gitops-bootstrap: $$c is in GITOPS_CHARTS but applications.yaml declares no Application for it" >&2; \
			exit 1; }; \
	done

## up: cluster, cilium, gateway, traefik-dashboard, hubble, istio, images, observability, kiali, rollouts, argocd, apps, gitops-bootstrap
# cilium is named here as well as inherited, so the one target everyone reads
# shows the CNI landing before anything that needs a schedulable node.
# istio before images and apps: once a namespace is labelled for injection, a
# pod created while istiod is still installing gets no sidecar and has to be
# deleted by hand to acquire one.
up: cluster cilium gateway traefik-dashboard hubble istio images
	@# Observability first only so the first requests are captured. The services
	@# do not depend on it: the OTel SDK logs an export failure and carries on,
	@# so a missing collector costs telemetry, not availability.
	@$(MAKE) --no-print-directory observability
	@$(MAKE) --no-print-directory kiali
	@# Before apps, not after: once a service is a Rollout, its chart fails to
	@# render against a cluster where the CRD is not registered yet.
	@$(MAKE) --no-print-directory rollouts
	@# The control plane before apps: once apps are handed to Argo CD it has to
	@# already be running to deploy them. The Applications themselves come after
	@# apps — see gitops-bootstrap.
	@$(MAKE) --no-print-directory argocd
	@$(MAKE) --no-print-directory apps
	@$(MAKE) --no-print-directory gitops-bootstrap
	@$(MAKE) --no-print-directory verify

## verify: prove the stack is actually working, not merely present
verify:
	@echo "── nodes ─────────────────────────────────────────"
	@kubectl get nodes -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[-1].type'
	@echo "── workloads ─────────────────────────────────────"
	@# Both namespaces. Listing one shows a healthy half while the other
	@# CrashLoops, and the storefront and the API fail independently now.
	@for ns in $(APP_NS); do \
		kubectl -n $$ns get pods \
			-o custom-columns='NS:.metadata.namespace,POD:.metadata.name,NODE:.spec.nodeName,STATUS:.status.phase'; \
	done
	@echo "── from the browser ──────────────────────────────"
	@# No -k on the https call. Passing without it means curl checked the chain
	@# against the system trust store, which is the same thing a browser does
	@# before showing a padlock.
	@curl -sS -o /dev/null -w '  http://localhost/   -> HTTP %{http_code}\n' --max-time 10 http://localhost/
	@curl -sS -o /dev/null -w '  https://localhost/  -> HTTP %{http_code}  (%{ssl_verify_result} = verified)\n' --max-time 10 https://localhost/
	@echo "── through every service ─────────────────────────"
	@# 401 without a token is the correct answer, and it proves the request
	@# reached auth rather than stopping at the gateway.
	@curl -sS -o /dev/null -w '  /api/products       -> HTTP %{http_code}  (401 = auth is enforced)\n' --max-time 10 https://localhost/api/products
	@curl -sS -o /dev/null -w '  /notification       -> HTTP %{http_code}\n' --max-time 10 https://localhost/notification
	@echo "   run 'make test' to buy something end to end"

## test: run every test suite
test:
	@./tests/run-all.sh

## test-unit: go test both modules — the local loop, no cluster required
test-unit:
	@# Suite equivalent is tests/unit.sh, which run-all.sh already runs, so
	@# test-unit is deliberately not called from test: — wiring it in would
	@# run every unit test twice. Without this target the only way to run
	@# these is `make test`, which needs the whole cluster up first.
	@cd apps/notification && go test ./...
	@cd apps/services && go test ./...

## test-preflight: does the running cluster match what the repo declares
test-preflight:
	@./tests/preflight.sh

## load-test: steady traffic for a canary analysis window (needs k6, ~4 min)
load-test:
	@# Not part of test: — it is a generator the Argo Rollouts analysis
	@# measures, not a pass/fail suite. Run it in the background while a
	@# canary rolls: without traffic in the window the success-rate query
	@# reads 0/0 and fails by design.
	@BASE=$${BASE:-https://localhost} k6 run tests/order-svc-canary-load.js

## test-routing: each path reaches the service that owns it
test-routing:
	@./tests/routing.sh

## test-route-isolation: every console host is answered by its own backend, never by a storefront route
test-route-isolation:
	@./tests/route-isolation.sh

## test-mesh: cilium is the cni and a pod with no rule is refused the database
test-mesh:
	@./tests/mesh.sh

## test-istio: every api pod carries a ready sidecar and traffic flows through it
test-istio:
	@./tests/istio.sh

## test-auth: credentials are checked and the token opens the api
test-auth:
	@./tests/auth.sh

## test-checkout: a customer buys a shirt, end to end
test-checkout:
	@./tests/checkout.sh

## test-resilience: the data and the service survive losing a pod
test-resilience:
	@./tests/resilience.sh

## test-o11y: the observability stack is up and receiving
test-o11y:
	@./tests/o11y-stack.sh

## test-journey: follow one purchase from log to trace to metric
test-journey:
	@./tests/o11y-journey.sh

## test-tls: capture the traffic and prove https encrypts it
test-tls:
	@./tests/tls-proof.sh $(CLUSTER)

## down: delete the cluster
down:
	@kind delete cluster --name $(CLUSTER)

.PHONY: help cluster cilium istio kiali namespaces gateway-api traefik tls gateway traefik-dashboard hubble secrets app-secrets \
        sql data images observability rollouts argocd gitops-bootstrap apps up verify down \
        test test-unit test-preflight load-test test-routing test-route-isolation test-mesh test-istio test-auth test-checkout test-resilience test-tls \
        test-o11y test-journey
