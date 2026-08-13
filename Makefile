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
	@kind get clusters | grep -qx $(CLUSTER) \
		&& echo "cluster $(CLUSTER) already exists" \
		|| kind create cluster --name $(CLUSTER) \
			--config platform/kind/cluster.yaml --wait 120s

## namespaces: create the namespaces everything else lands in
namespaces: cluster
	@kubectl apply -f $(MANIFESTS)/01-namespaces.yaml

GATEWAY_API_VERSION := v1.6.1

## gateway-api: install Gateway API CRDs — must run before Traefik
gateway-api: cluster
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
	@kubectl apply -f $(MANIFESTS)/02-certificates.yaml
	@kubectl -n $(TRAEFIK_NS) wait --for=condition=Ready --timeout=120s \
		certificate/platform-tls

## gateway: attach the routes to the Gateway
gateway: namespaces tls
	@# Nothing to apply first — the Gateway comes from the Traefik release. It is
	@# created before the certificate exists, so the wait for Programmed belongs
	@# here, after tls, not inside the traefik target.
	@kubectl -n $(TRAEFIK_NS) wait --for=condition=Programmed --timeout=180s gateway/platform
	@kubectl apply -f $(MANIFESTS)/04-routes.yaml

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
	@# charts grant reaches nothing until the matching ingress in 11 exists. Left
	@# to a later target, every query out of $(API_NS) is dropped for the length
	@# of the gap and reads as a database outage rather than a missing manifest.
	@kubectl apply -f $(MANIFESTS)/05-mariadb.yaml -f $(MANIFESTS)/06-redis.yaml \
		-f $(MANIFESTS)/11-netpol-data.yaml
	@kubectl -n $(DATA_NS) rollout status statefulset/mariadb --timeout=180s
	@kubectl -n $(DATA_NS) rollout status deployment/redis --timeout=120s
	@# The schema Job runs separately from the database, so waiting on the
	@# StatefulSet alone would let a service start against empty tables.
	@kubectl -n $(DATA_NS) wait --for=condition=complete job/mariadb-init --timeout=180s

# One image per service out of apps/services, selected by the SVC build arg.
GO_SVCS := auth-svc order-svc payment-svc mock-payment

## images: build every image and load it into the cluster
images:
	@# pullPolicy is Never, so a chart pinned to an unbuilt tag does not fail at
	@# deploy time — the pod just sits in ErrImageNeverPull.
	@./platform/scripts/check-image-tags.sh $(VERSION)
	@docker build --build-arg VERSION=$(VERSION) -t dummy:$(VERSION) apps/dummy
	@for s in $(GO_SVCS); do \
		docker build --build-arg SVC=$$s --build-arg VERSION=$(VERSION) \
			-t $$s:$(VERSION) apps/services; \
	done
	@docker build -t web-ui:$(VERSION) apps/web-ui
	@# kind nodes have their own image store and no registry, so an image built on
	@# the host is invisible until loaded in.
	@kind load docker-image --name $(CLUSTER) \
		dummy:$(VERSION) web-ui:$(VERSION) \
		$(foreach s,$(GO_SVCS),$(s):$(VERSION))

# One release per service, so one can be rolled back without its siblings.
# platform-config first: a pod that mounts a ConfigMap which does not exist yet
# stays Pending rather than failing loudly.
APP_CHARTS := platform-config auth-svc order-svc payment-svc mock-payment web-ui dummy

# platform-config renders only a ConfigMap, so there is nothing to wait on.
APP_DEPLOYS := auth-svc order-svc payment-svc mock-payment web-ui dummy

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
	@kubectl apply -f $(MANIFESTS)/07-observability.yaml
	@# Before the components, not after: applied last, a rule that selects the
	@# wrong label drops exports without failing anything — the OTel SDK logs the
	@# failure and keeps serving, so the only symptom is empty dashboards. Applied
	@# here, the same mistake shows up in the rollout waits at the end of this
	@# target. The namespace has to exist first, which is what 07 creates.
	@kubectl apply -f $(MANIFESTS)/12-netpol-observability.yaml
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
	@kubectl apply -f $(MANIFESTS)/08-grafana-route.yaml

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

## argocd: install the Argo CD control plane, served at /argocd
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
	@kubectl apply -f $(MANIFESTS)/09-argocd-route.yaml

## up: cluster, gateway, images, observability, rollouts, argocd, apps
up: cluster gateway images
	@# Observability first only so the first requests are captured. The services
	@# do not depend on it: the OTel SDK logs an export failure and carries on,
	@# so a missing collector costs telemetry, not availability.
	@$(MAKE) --no-print-directory observability
	@# Before apps, not after: once a service is a Rollout, its chart fails to
	@# render against a cluster where the CRD is not registered yet.
	@$(MAKE) --no-print-directory rollouts
	@# Before apps: once apps are handed to Argo CD it has to already be running
	@# to deploy them, and the ordering should not have to change then.
	@$(MAKE) --no-print-directory argocd
	@$(MAKE) --no-print-directory apps
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
	@# reached auth-svc rather than stopping at the gateway.
	@curl -sS -o /dev/null -w '  /api/products       -> HTTP %{http_code}  (401 = auth is enforced)\n' --max-time 10 https://localhost/api/products
	@curl -sS -o /dev/null -w '  /dummy              -> HTTP %{http_code}\n' --max-time 10 https://localhost/dummy
	@echo "   run 'make test' to buy something end to end"

## test: run every test suite
test:
	@./tests/run-all.sh

## test-routing: each path reaches the service that owns it
test-routing:
	@./tests/routing.sh

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

.PHONY: help cluster namespaces gateway-api traefik tls gateway secrets app-secrets \
        sql data images observability rollouts argocd apps up verify down \
        test test-routing test-auth test-checkout test-resilience test-tls \
        test-o11y test-journey
