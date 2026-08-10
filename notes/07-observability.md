# Observability — metrics, logs and traces from one pipeline

**Date:** 2026-08-10
**Status:** done — `make test-o11y` (14 checks) and `make test-journey` (6 checks)

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

**Prometheus scrapes nothing here — Alloy writes to it.**

- `web.enable-remote-write-receiver` in `values/prometheus.yaml`; off by default,
  and without it Alloy's writes get a 404 with nothing in any log to say why
- every chart-default scrape job is disabled by name — a dozen `kubernetes_sd`
  jobs failing against RBAC this release does not grant

**Why push** — one export path for all three signals. A scrape model needs
`/metrics` on every service plus a second path for logs and traces; the SDK
already emits all three over OTLP.

**Cost** — telemetry is batched, so nothing is queryable the instant a request
returns. Both test suites wait 25–30s rather than retrying until data appears,
which would hide a pipeline that had genuinely stopped.

---

## The alias every service points at

**Services resolve `otel-collector`, not `alloy`** —
`platform/manifests/07-observability.yaml` is a Service with no selector of its
own name, pointing at Alloy's pods.

- one address in `charts/apps/platform-config/values.yaml`:
  `OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector.observability.svc.cluster.local:4317`
- swapping Alloy for an OpenTelemetry Collector is a Service edit, not a rebuild
  of six images

**Failure mode it introduces** — an alias with no endpoints resolves fine and
drops everything. The services keep serving and record nothing. `o11y-stack.sh`
checks the endpoint list for exactly this.

---

## Correlation — the part that makes it usable

The storefront hands back the ids needed to go dig, on the request that failed:

![failed checkout with trace_id/order_id/user_id/session_id exposed](screenshot/o11y/Screenshot%202569-08-11%20at%2001.16.13.png)

**A trace id comes back in the response header.** `traceresponse` on every
reply, so a specific request is findable afterwards instead of searching by
time.

**Three links are provisioned, not configured by hand** (`values/grafana.yaml`):

| From | To | How |
|---|---|---|
| log line | its trace | `derivedFields` regex on `traceid` — two patterns, logs arrive as JSON and as logfmt |
| span | its logs | `tracesToLogsV2`, filtered by `deployment_environment` |
| latency graph | a slow trace | `exemplarTraceIdDestinations` |

A Loki entry carries its `traceid` as a first-class field, not buried in text —
the paired ERROR log below it shares the same id:

![log line and its trace id side by side in Loki](screenshot/o11y/Screenshot%202569-08-11%20at%2001.21.13.png)

**Context propagation is the application's job** — Istio does not do it. The
services pass `traceparent` themselves, and one trace spanning payment-svc →
order-svc → mock-payment is the evidence that they do:

![Tempo span tree: payment-svc calls order-svc, then mock-payment, ends 402](screenshot/o11y/Screenshot%202569-08-11%20at%2001.17.25.png)

![TraceQL query by session_id returning every trace in that checkout](screenshot/o11y/Screenshot%202569-08-11%20at%2001.17.08.png)

---

## Verification

```sh
make test-o11y      # 14 checks — the stack is assembled and receiving
make test-journey   # 6 checks  — one purchase, followed end to end
```

`o11y-stack.sh` asks whether the pipeline is wired. `o11y-journey.sh` buys a
shirt in five requests, then asks whether the platform can say what happened —
and prints Grafana links for that exact run.

What the journey suite proves, in order:

| Check | Fails when |
|---|---|
| order settled `paid` | the purchase itself broke |
| `traceresponse` header present | middleware is not returning the trace id |
| one trace spans three services | context is not propagating |
| database spans exist | gorm/redis instrumentation never reached the collector |
| log lines carry that trace id | logs and traces are not correlated |
| request counts per service | remote-write or the label conversion is off |
