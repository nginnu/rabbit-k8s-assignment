# Observability — metrics, logs and traces from one pipeline

**Date:** 2026-08-10
**Status:** done — `make test-o11y` (16 checks) and `make test-journey` (8 checks)

---

## The shape

Every service exports OTLP to one collector. The collector splits the three
signals and forwards each to its own store.

```
[ auth / order / payment / mock-payment / web-ui ]
                    │
                    │  OTLP gRPC :4317   (push, every 10s)
                    ▼
            [ alloy ]  ── alias Service: otel-collector
                    │
        ┌───────────┼───────────┐
        ▼           ▼           ▼
   [ prometheus ] [ loki ]  [ tempo ]
        │           │           │
        └───────────┴───────────┘
                    ▼
              [ grafana ]  ──►  https://localhost/grafana
```

| Component | Chart | Version |
|---|---|---|
| alloy | alloy-1.11.1 | v1.18.1 |
| prometheus | prometheus-29.23.0 | v3.13.2 |
| loki | loki-7.2.0 | 3.6.11 |
| tempo | tempo-1.24.4 | 2.9.0 |
| grafana | grafana-10.5.15 | 12.3.1 |

![RED/USE metrics per service on one dashboard](screenshot/o11y/Screenshot%202569-08-11%20at%2001.20.50.png)

---

## Push, not scrape

Prometheus scrapes nothing — Alloy pushes to it.

- `web.enable-remote-write-receiver` on (`values/prometheus.yaml`), off by
  default — without it, Alloy's writes 404 with no log saying why
- every chart-default scrape job disabled by name — otherwise a dozen
  `kubernetes_sd` jobs fail against RBAC this release doesn't grant
- why: one export path for all three signals, since the OTLP SDK already
  emits metrics, logs and traces together — scrape needs `/metrics` plus a
  separate path for the other two

---

## The alias every service points at

Services resolve `otel-collector`, not `alloy` — `07-observability.yaml` is a
Service pointing at Alloy's pods under a name that isn't its own.

- one address, set once: `OTEL_EXPORTER_OTLP_ENDPOINT` in `platform-config`
- swapping Alloy for an OTel Collector later is a Service edit, not a rebuild
  of six images
- risk: an alias with no endpoints resolves fine and silently drops
  everything — services keep serving, nothing gets recorded.
  `o11y-stack.sh` checks the endpoint list for exactly this

---

## Correlation — the part that makes it usable

The storefront returns trace_id/order_id/user_id/session_id on a failed
checkout — the ids needed to go dig:

![failed checkout with trace_id/order_id/user_id/session_id exposed](screenshot/o11y/Screenshot%202569-08-11%20at%2001.16.13.png)

Every reply carries a `traceresponse` header, so one request is findable
afterwards instead of searched for by time.

Three links, provisioned in `values/grafana.yaml`, not clicked together by
hand:

| From | To | How |
|---|---|---|
| log line | its trace | `derivedFields` regex on `traceid` — JSON and logfmt both matched |
| span | its logs | `tracesToLogsV2`, filtered by `deployment_environment` |
| latency graph | a slow trace | `exemplarTraceIdDestinations` |

`traceid` is a first-class Loki field, not buried in text — the paired ERROR
log below shares it:

![log line and its trace id side by side in Loki](screenshot/o11y/Screenshot%202569-08-11%20at%2001.21.13.png)

Context propagation is the application's job, not Istio's — services pass
`traceparent` themselves. One trace spanning payment-svc → order-svc →
mock-payment is the evidence:

![Tempo span tree: payment-svc calls order-svc, then mock-payment, ends 402](screenshot/o11y/Screenshot%202569-08-11%20at%2001.17.25.png)

![TraceQL query by session_id returning every trace in that checkout](screenshot/o11y/Screenshot%202569-08-11%20at%2001.17.08.png)

---

## Verification

```sh
make test-o11y      # 16 checks — the stack is assembled and receiving
make test-journey   #  8 checks — one purchase, followed end to end
```

`o11y-stack.sh` asks whether the pipeline is wired. `o11y-journey.sh` buys a
shirt in five requests, asks whether the platform can say what happened, and
prints Grafana links for that run.

What the journey suite proves, in order:

| Check | Fails when |
|---|---|
| order settled `paid` | the purchase itself broke |
| `traceresponse` header present | middleware is not returning the trace id |
| one trace spans three services | context is not propagating |
| database spans exist | gorm/redis instrumentation never reached the collector |
| log lines carry that trace id | logs and traces are not correlated |
| request counts per service | remote-write or the label conversion is off |
