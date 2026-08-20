export PATH := /usr/local/bin:$(PATH)

CLUSTER ?= rabbit-k8s-test
VERSION ?= v0.1.0

LOCAL      := k8s-local
SCRIPTS    := ./scripts
GITOPS     ?= ../rabbit-gitops
API_REPO   ?= ../rabbit-api
WEB_REPO   ?= ../rabbit-web
CHARTS         := $(GITOPS)/charts
GITOPS_SCRIPTS := $(GITOPS)/scripts
GATEWAY_NS := gateway

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F: '{printf "  \033[36m%-16s\033[0m%s\n", $$1, $$2}'

## preflight: fail before the cluster when a sibling repo is not cloned
preflight:
	@missing=0; \
	for repo in \
		"rabbit-api:API_REPO:$(API_REPO)" \
		"rabbit-web:WEB_REPO:$(WEB_REPO)" \
		"rabbit-gitops:GITOPS:$(GITOPS)"; do \
		name="$${repo%%:*}"; rest="$${repo#*:}"; \
		var="$${rest%%:*}"; path="$${rest#*:}"; \
		[ -d "$$path" ] && continue; \
		echo "$$name not found at $$path"; \
		echo "    git clone https://github.com/nginnu/$$name.git"; \
		echo "    or, if it is cloned elsewhere: make $(MAKECMDGOALS) $$var=<path>"; \
		missing=1; \
	done; \
	[ "$$missing" -eq 0 ] || exit 1
	@echo "preflight: rabbit-api, rabbit-web, rabbit-gitops present"

# Read out of cluster.yaml rather than pinned again here: kind takes the node
# image from that file, and a second copy of the number is a second place to
# forget. Every other component in this Makefile pins its own version because
# nothing else reads one from a file.
K8S_VERSION := $(shell sed -n 's|.*kindest/node:\(v[0-9.]*\)@.*|\1|p' $(LOCAL)/cluster.yaml | head -1)

## cluster: create the kind cluster
cluster: preflight
	@kind get clusters | grep -qx $(CLUSTER) \
		&& echo "cluster $(CLUSTER) already exists" \
		|| kind create cluster --name $(CLUSTER) \
			--config $(LOCAL)/cluster.yaml
	@# A cluster created before the node image was pinned keeps running on
	@# whatever version it was built with, and every check after this one
	@# passes against the wrong Kubernetes.
	@$(SCRIPTS)/check-version.sh kubernetes $(K8S_VERSION) \
		"$$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.kubeletVersion}')"

CILIUM_VERSION := 1.20.0

## cilium: install the CNI — no pod schedules anywhere until this runs
cilium: cluster
	@helm upgrade --install cilium oci://quay.io/cilium/charts/cilium \
		-n kube-system --version $(CILIUM_VERSION) \
		-f $(LOCAL)/cilium/values.yaml \
		--wait --timeout 10m
	@$(SCRIPTS)/check-version.sh cilium v$(CILIUM_VERSION) \
		"$$(kubectl -n kube-system get daemonset cilium \
			-o jsonpath='{.spec.template.spec.containers[?(@.name=="cilium-agent")].image}' \
			| sed 's/@.*//; s/.*://')"
	@kubectl wait --for=condition=Ready nodes --all --timeout=180s

GATEWAY_API_VERSION := v1.6.1

## gateway-api: install Gateway API CRDs — must run before Traefik
gateway-api: cilium
	@kubectl apply --server-side --force-conflicts -f \
		https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml
	@kubectl wait --for=condition=established --timeout=60s \
		crd/gateways.gateway.networking.k8s.io \
		crd/httproutes.gateway.networking.k8s.io \
		crd/gatewayclasses.gateway.networking.k8s.io
	@$(SCRIPTS)/check-version.sh gateway-api $(GATEWAY_API_VERSION) \
		"$$(kubectl get crd gateways.gateway.networking.k8s.io \
			-o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}')"

## networking: create the gateway namespace and the GatewayClass Traefik answers to
networking: gateway-api
	@kubectl apply -f $(LOCAL)/networking/namespace.yaml
	@kubectl apply -f $(LOCAL)/networking/gatewayclass.yaml

TRAEFIK_VERSION     := 41.2.0
TRAEFIK_APP_VERSION := v3.7.10

## traefik: install the controller that answers for the GatewayClass
traefik: networking
	@helm repo add traefik https://traefik.github.io/charts >/dev/null 2>&1 || true
	@helm repo update traefik >/dev/null
	@helm upgrade --install traefik traefik/traefik -n $(GATEWAY_NS) \
		--version $(TRAEFIK_VERSION) -f $(LOCAL)/traefik/values.yaml \
		--wait --timeout 5m
	@$(SCRIPTS)/check-version.sh traefik $(TRAEFIK_APP_VERSION) \
		"$$(kubectl -n $(GATEWAY_NS) get deploy traefik \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

CERT_MANAGER_VERSION := v1.21.1

## tls: install cert-manager and load the local CA it issues from
tls: traefik
	@helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
	@helm repo update jetstack >/dev/null
	@helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager \
		--create-namespace --version $(CERT_MANAGER_VERSION) \
		--set crds.enabled=true --wait --timeout 5m
	@$(SCRIPTS)/seed-ca.sh
	@kubectl apply -f $(LOCAL)/cert-manager/clusterissuer.yaml
	@kubectl apply -f $(LOCAL)/networking/certificate.yaml
	@kubectl -n $(GATEWAY_NS) wait --for=condition=Ready --timeout=120s \
		certificate/gateway-tls

## gateway: create the Gateway and wait for Traefik to program it
gateway: tls
	@kubectl apply -f $(LOCAL)/networking/gateway.yaml
	@kubectl -n $(GATEWAY_NS) wait --for=condition=Programmed --timeout=180s \
		gateway/external

NAMESPACES := \
	$(LOCAL)/apps/namespace.yaml \
	$(LOCAL)/storage/namespace.yaml \
	$(LOCAL)/argocd/namespace.yaml \
	$(LOCAL)/observability/namespace.yaml

APP_NETPOL := \
	$(LOCAL)/apps/deny-all-ingress.yaml \
	$(LOCAL)/apps/deny-all-egress.yaml \
	$(LOCAL)/apps/allow-dns.yaml \
	$(LOCAL)/apps/allow-monitoring.yaml

## namespaces: create the namespaces everything else lands in
namespaces: cilium
	@for f in $(NAMESPACES); do \
		kubectl apply -f $$f || exit 1; \
	done
	@for f in $(APP_NETPOL); do \
		kubectl apply -f $$f || exit 1; \
	done

ISTIO_VERSION := 1.30.3

## istio: install the mesh control plane — it injects nothing until a namespace asks
istio: cilium
	@helm repo add istio https://istio-release.storage.googleapis.com/charts >/dev/null 2>&1 || true
	@helm repo update istio >/dev/null
	@helm upgrade --install istio-base istio/base \
		-n istio-system --create-namespace --version $(ISTIO_VERSION) \
		--wait --timeout 5m
	@helm upgrade --install istiod istio/istiod \
		-n istio-system --version $(ISTIO_VERSION) \
		-f $(LOCAL)/istio/values.yaml \
		--wait --timeout 5m
	@$(SCRIPTS)/check-version.sh istiod $(ISTIO_VERSION) \
		"$$(kubectl -n istio-system get deploy istiod \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"
	@kubectl apply -f $(LOCAL)/istio/destination-rule.yaml

## routes: attach every platform route to the Gateway
routes: gateway
	@kubectl apply -f $(LOCAL)/traefik/route.yaml
	@kubectl apply -f $(LOCAL)/cilium/route.yaml

DATA_NS := data
WEB_NS  := web
API_NS  := api
APP_NS  := $(WEB_NS) $(API_NS)

## secrets: generate credentials into the cluster, never into git
secrets: namespaces
	@kubectl -n $(DATA_NS) get secret mariadb >/dev/null 2>&1 \
		&& echo "secret mariadb already exists — delete it to rotate" \
		|| kubectl -n $(DATA_NS) create secret generic mariadb \
			--from-literal=username=rabbitshop \
			--from-literal=password=$$(openssl rand -hex 16) \
			--from-literal=root-password=$$(openssl rand -hex 16)

## app-secrets: build the app credentials from the database ones
app-secrets: secrets
	@DBPASS=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.password}' | base64 -d); \
	DBUSER=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.username}' | base64 -d); \
	kubectl -n $(API_NS) get secret app-secrets >/dev/null 2>&1 \
		|| kubectl -n $(API_NS) create secret generic app-secrets \
			--from-literal=mariadb-dsn="$$DBUSER:$$DBPASS@tcp(mariadb.$(DATA_NS).svc.cluster.local:3306)/rabbitshop?parseTime=true" \
			--from-literal=jwt-secret=$$(openssl rand -hex 32)

## sql: load the schema and seed into a ConfigMap
sql: namespaces
	@kubectl -n $(DATA_NS) create configmap mariadb-init-sql \
		--from-file=init.sql=$(LOCAL)/storage/mariadb-init.sql \
		--dry-run=client -o yaml | kubectl apply -f -

## data: bring up MariaDB and Redis
data: secrets sql
	@kubectl apply -f $(LOCAL)/storage/mariadb.yaml \
		-f $(LOCAL)/storage/redis.yaml \
		-f $(LOCAL)/storage/netpol.yaml
	@kubectl -n $(DATA_NS) rollout status statefulset/mariadb --timeout=180s
	@kubectl -n $(DATA_NS) rollout status deployment/redis --timeout=120s
	@kubectl -n $(DATA_NS) wait --for=condition=complete job/mariadb-init --timeout=180s

GO_SVCS := auth catalog order payment

## images: build every image and load it into the cluster
images: preflight
	@$(GITOPS_SCRIPTS)/check-image-tags.sh $(VERSION)
	@docker build --build-arg VERSION=$(VERSION) -t notification:$(VERSION) $(API_REPO)/notification-api
	@for s in $(GO_SVCS); do \
		docker build --build-arg SVC=$$s --build-arg VERSION=$(VERSION) \
			-t $$s:$(VERSION) $(API_REPO)/shop-api; \
	done
	@docker build -t web-ui:$(VERSION) $(WEB_REPO)
	@kind load docker-image --name $(CLUSTER) \
		notification:$(VERSION) web-ui:$(VERSION) \
		$(foreach s,$(GO_SVCS),$(s):$(VERSION))

APP_CHARTS  := platform-config auth catalog order payment notification web-ui
APP_DEPLOYS := auth catalog order payment notification web-ui

## apps: deploy the application services from their charts
apps: data app-secrets
	@for c in $(APP_CHARTS); do \
		ns=$$($(GITOPS_SCRIPTS)/chart-namespace.sh $$c) || exit 1; \
		helm dependency update $(CHARTS)/$$c >/dev/null; \
		helm upgrade --install $$c $(CHARTS)/$$c -n $$ns; \
	done
	@for d in $(APP_DEPLOYS); do \
		ns=$$($(GITOPS_SCRIPTS)/chart-namespace.sh $$d) || exit 1; \
		kubectl -n $$ns rollout status deployment/$$d --timeout=180s; \
	done
	@kubectl delete httproute api -n api --ignore-not-found

PROMETHEUS_VERSION := 29.23.0
LOKI_VERSION       := 7.2.0
TEMPO_VERSION      := 1.24.4
ALLOY_VERSION      := 1.11.1
GRAFANA_VERSION    := 10.5.15

OBS := $(LOCAL)/observability

## observability: prometheus, loki, tempo, alloy and grafana
observability: namespaces
	@kubectl apply -f $(OBS)/alloy/collector-service.yaml
	@kubectl apply -f $(OBS)/netpol.yaml
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo add grafana https://grafana.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo update prometheus-community grafana >/dev/null
	@kubectl -n observability create configmap grafana-dashboards \
		--from-file=platform.json=$(OBS)/grafana/dashboard.json \
		--dry-run=client -o yaml | kubectl apply -f -
	@helm upgrade --install prometheus prometheus-community/prometheus \
		-n observability --version $(PROMETHEUS_VERSION) -f $(OBS)/prometheus/values.yaml
	@helm upgrade --install loki grafana/loki \
		-n observability --version $(LOKI_VERSION) -f $(OBS)/loki/values.yaml
	@helm upgrade --install tempo grafana/tempo \
		-n observability --version $(TEMPO_VERSION) -f $(OBS)/tempo/values.yaml
	@helm upgrade --install alloy grafana/alloy \
		-n observability --version $(ALLOY_VERSION) -f $(OBS)/alloy/values.yaml
	@helm upgrade --install grafana grafana/grafana \
		-n observability --version $(GRAFANA_VERSION) -f $(OBS)/grafana/values.yaml
	@kubectl -n observability rollout status deployment/alloy --timeout=180s
	@kubectl -n observability rollout status deployment/grafana --timeout=180s
	@kubectl -n observability rollout status statefulset/loki --timeout=180s
	@kubectl apply -f $(OBS)/grafana/route.yaml

KIALI_VERSION := 2.30.0

## kiali: mesh console — the graph of who talks to whom, over the running mesh
kiali: istio observability
	@helm repo add kiali https://kiali.org/helm-charts >/dev/null 2>&1 || true
	@helm repo update kiali >/dev/null
	@helm upgrade --install kiali kiali/kiali-server \
		-n observability --version $(KIALI_VERSION) \
		-f $(OBS)/kiali/values.yaml \
		--wait --timeout 5m
	@$(SCRIPTS)/check-version.sh kiali $(KIALI_VERSION) \
		"$$(kubectl -n observability get deploy kiali \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://; s/^v//')"
	@kubectl apply -f $(OBS)/kiali/route.yaml

ROLLOUTS_VERSION     := 2.41.1
ROLLOUTS_APP_VERSION := v1.9.1

## rollouts: install the Argo Rollouts controller
rollouts: namespaces
	@helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	@helm repo update argo >/dev/null
	@helm upgrade --install argo-rollouts argo/argo-rollouts \
		-n argo-rollouts --create-namespace --version $(ROLLOUTS_VERSION)
	@kubectl -n argo-rollouts rollout status deployment/argo-rollouts --timeout=180s
	@$(SCRIPTS)/check-version.sh argo-rollouts $(ROLLOUTS_APP_VERSION) \
		"$$(kubectl -n argo-rollouts get deploy argo-rollouts \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

ARGOCD_VERSION     := 10.3.2
ARGOCD_APP_VERSION := v3.5.0

## argocd: install the Argo CD control plane, served at https://argocd.localhost
argocd: namespaces
	@helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	@helm repo update argo >/dev/null
	@helm upgrade --install argocd argo/argo-cd \
		-n argocd --create-namespace --version $(ARGOCD_VERSION) \
		-f $(LOCAL)/argocd/values.yaml
	@kubectl -n argocd rollout status deployment/argocd-server --timeout=300s
	@$(SCRIPTS)/check-version.sh argocd $(ARGOCD_APP_VERSION) \
		"$$(kubectl -n argocd get deploy argocd-server \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"
	@kubectl apply -f $(LOCAL)/argocd/route.yaml

## verify: prove the stack is actually working, not merely present
verify:
	@echo "── nodes ─────────────────────────────────────────"
	@kubectl get nodes -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[-1].type'
	@echo "── workloads ─────────────────────────────────────"
	@for ns in $(APP_NS); do \
		kubectl -n $$ns get pods \
			-o custom-columns='NS:.metadata.namespace,POD:.metadata.name,NODE:.spec.nodeName,STATUS:.status.phase'; \
	done
	@echo "── from the browser ──────────────────────────────"
	@curl -sS -o /dev/null -w '  http://localhost/   -> HTTP %{http_code}' --max-time 10 http://localhost/; echo
	@curl -sS -o /dev/null -w '  https://localhost/  -> HTTP %{http_code}  (%{ssl_verify_result} = verified)' --max-time 10 https://localhost/; echo
	@curl -sS -o /dev/null -w '  /api/products       -> HTTP %{http_code}  (401 = auth is enforced)' --max-time 10 https://localhost/api/products; echo

## up: everything, in order
up: cluster cilium namespaces gateway routes istio images
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory observability
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory kiali
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory rollouts
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory argocd
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory apps
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory verify

## down: delete the cluster
down:
	@kind delete cluster --name $(CLUSTER)

.PHONY: help preflight cluster cilium gateway-api networking traefik tls gateway routes namespaces istio secrets app-secrets sql data images apps observability kiali rollouts argocd verify up down
