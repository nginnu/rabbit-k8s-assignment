{{/*
NetworkPolicy — off by default. `networkPolicy: true` renders one policy per
service: default-deny both directions, with the peers named in
`networkPolicy.ingress` / `networkPolicy.egress` allowed back through.

Peers are names, not selectors. Every tier this platform talks to labels itself
differently — the gateway is the Traefik controller pod in its own namespace,
not anything named after the Gateway object the routes point at — so a values
file holding raw selectors would repeat four different label conventions in six
charts. A wrong selector
does not fail: it renders a valid policy that matches no pod, and the traffic
is dropped with nothing in the object to look at. The name is checked here
instead, and an unknown one stops the render.

The other trap this hides is YAML shape. Inside one `from` entry, a
namespaceSelector and a podSelector at the same level mean AND (those pods in
that namespace); split across two list entries they mean OR (all pods in that
namespace, or those pods here). One dash is the difference between reaching
mariadb and reaching every pod in the data namespace.
*/}}

{{/*
The peer catalogue. Each entry renders one `from`/`to` element plus its ports.

Cross-namespace peers select on kubernetes.io/metadata.name, which the API
server sets on every namespace and nobody can forget to apply — the hand-written
labels in platform/manifests/apps/web/namespace.yaml and
platform/manifests/apps/api/namespace.yaml are not on kube-system, and the
traefik namespace is created by helm --create-namespace, which labels nothing.
*/}}
{{- define "platform-service.networkpolicy.peer" -}}
{{- $peer := .peer -}}
{{- $ns := .ns -}}
{{- if eq $peer "gateway" }}
{{/*
  Traefik is the controller and the data path in one pod: there is no
  per-Gateway deployment and nothing carries a gateway.networking.k8s.io label,
  so a rule written against the Gateway object's name matches no pod and every
  service answers the edge with a timeout that reads as 503 at the browser.

  Selected on name alone, not app.kubernetes.io/instance: instance is
  <release>-<namespace>, so naming it would couple this rule to the release
  name in the Makefile. Nothing else runs in that namespace.
*/}}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: traefik
    podSelector:
      matchLabels:
        app.kubernetes.io/name: traefik
{{- else if eq $peer "dns" }}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: kube-system
    podSelector:
      matchLabels:
        k8s-app: kube-dns
  ports:
  - protocol: UDP
    port: 53
  - protocol: TCP
    port: 53
{{- else if eq $peer "istiod" }}
{{/*
  15012 only. The sidecar pulls its whole configuration and its certificate over
  this one port, and it is the first thing it does — with the port closed the
  proxy never becomes ready, the pod never leaves 1/2, and the rollout stalls on
  a service that has nothing wrong with it.

  15010 is the plaintext form of the same stream and stays closed: istiod serves
  both, so opening it turns a single typo in a proxy config into an unencrypted
  certificate exchange that still works and is never noticed.
*/}}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: istio-system
    podSelector:
      matchLabels:
        app.kubernetes.io/name: istiod
  ports:
  - protocol: TCP
    port: 15012
{{- else if eq $peer "alloy" }}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: observability
    podSelector:
      matchLabels:
        app.kubernetes.io/name: alloy
  ports:
  - protocol: TCP
    port: 4317
{{- else if eq $peer "mariadb" }}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: data
    podSelector:
      matchLabels:
        app.kubernetes.io/name: mariadb
  ports:
  - protocol: TCP
    port: 3306
{{- else if eq $peer "redis" }}
- from_or_to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: data
    podSelector:
      matchLabels:
        app.kubernetes.io/name: redis
  ports:
  - protocol: TCP
    port: 6379
{{- else if hasPrefix "svc:" $peer }}
{{- $target := trimPrefix "svc:" $peer }}
- from_or_to:
  - podSelector:
      matchLabels:
        app.kubernetes.io/name: {{ $target }}
{{- else }}
{{- fail (printf "networkPolicy: unknown peer %q on a service in namespace %s — use gateway, dns, istiod, alloy, mariadb, redis, or svc:<name>" $peer $ns) }}
{{- end }}
{{- end -}}

{{/*
Renders a rule list for one direction. `from_or_to` above is a placeholder the
caller rewrites, so the peer catalogue is written once instead of twice.
*/}}
{{- define "platform-service.networkpolicy.rules" -}}
{{- $key := .key -}}
{{- $ns := .ns -}}
{{- range $peer := .peers }}
{{- $rule := include "platform-service.networkpolicy.peer" (dict "peer" $peer "ns" $ns) -}}
{{ $rule | replace "from_or_to" $key }}
{{- end }}
{{- end -}}

{{- define "platform-service.networkpolicy" -}}
{{- range $name, $svc := .Values.services }}
{{- if $svc.networkPolicy }}
{{- $ns := include "platform-service.namespace" $ }}
{{- $np := $svc.networkPolicyPeers | default dict }}
{{- $ingress := $np.ingress | default list }}
{{/*
  DNS is injected, never declared. A policy with Egress in policyTypes denies
  everything not listed, and the first thing any of these services does is
  resolve mariadb — so a values file that forgets dns produces a pod that
  cannot look up its own database and reports it as a connection failure.
  Leaving it to be typed six times means it gets forgotten once.
*/}}
{{- $egress := concat ($np.egress | default list) (list "dns") -}}
{{/*
  istiod is injected off `mesh`, the same way dns is injected unconditionally,
  and for the same reason: it is a peer nobody remembers a sidecar needs. Tying
  it to the flag that adds the sidecar means the allow rule cannot be forgotten
  in the one edit that makes it necessary.
*/}}
{{- if $svc.mesh }}{{- $egress = concat $egress (list "istiod") -}}{{- end }}
{{- $egress = $egress | uniq }}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ $name }}
  namespace: {{ $ns }}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{ $name }}
  {{/*
    Both types are always listed. Naming a type with an empty rule list is what
    denies that direction; omitting Egress would leave egress unrestricted and
    the policy would look complete while enforcing half of it.
  */}}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    {{- if $ingress }}
    {{- include "platform-service.networkpolicy.rules" (dict "peers" $ingress "key" "from" "ns" $ns) | trim | nindent 4 }}
    {{- else }} []
    {{- end }}
  egress:
    {{- include "platform-service.networkpolicy.rules" (dict "peers" $egress "key" "to" "ns" $ns) | trim | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}
