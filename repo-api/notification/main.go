// Command notification is the notification service. payment-svc POSTs /notify
// when a payment settles; GET /notifications reads back what was received, so
// the checkout test can prove the call happened rather than trust payment-svc's
// own logs. POST /notify-failed deliberately fails loudly — a burst of error
// logs and a 500 — so a failing notification can be simulated and observed in
// Loki.
//
// A real service cannot be told to fail on demand, so it cannot prove
// auto-rollback. This one can: /chaos/error-rate and /chaos/latency change how
// it behaves at runtime, without a redeploy.
//
// Telemetry is push-based, same as its siblings: traces, metrics and logs all
// go over OTLP/gRPC to the alloy collector. Alloy neither reads container
// stdout beyond the k8s events it is given nor scrapes app metrics, so a
// prometheus /metrics endpoint here would be dead weight nobody collects. The
// OTel setup is a trimmed copy of the services' shared/otel + shared/logger —
// this is a separate module and cannot import them, and for one service the
// copy is smaller than a shared module would be.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// serviceShortName feeds the OTel service.name resource: <prefix>-<env>-notification,
// the same shape its siblings build. The dashboards parse that shape, so a
// mismatch here makes their queries return zero series with no error.
const serviceShortName = "notification"

// Set by otelInit, read by newLogger and the otelhttp wrap — one init order in
// main, no provider plumbing through every constructor.
var (
	loggerProvider *sdklog.LoggerProvider
	meterProvider  *sdkmetric.MeterProvider
)

// otelInit registers the tracer, meter and logger providers globally, all
// exporting over OTLP/gRPC. Required envs are read up front: a missing
// collector endpoint or environment fails the pod at start, not at first
// export — the same contract as the services' config.LoadBase.
//
// Export failures themselves stay non-fatal: the batch exporters retry in the
// background and drop on timeout, so a collector that is down costs telemetry,
// not availability.
//
// deployment.environment is deliberately not sent — Alloy derives it from
// service.name, so setting it here would produce two sources for one label.
func otelInit(ctx context.Context) (func(context.Context) error, error) {
	endpoint := mustEnv("OTEL_EXPORTER_OTLP_ENDPOINT")
	deploymentEnv := mustEnv("DEPLOYMENT_ENV")
	prefix := env("SVC_PREFIX", "platform")
	svcVersion := env("SVC_VERSION", version) // ldflags version is the real build

	serviceName := fmt.Sprintf("%s-%s-%s", prefix, deploymentEnv, serviceShortName)

	hostname, _ := os.Hostname()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(svcVersion),
			semconv.ServiceInstanceID(hostname),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// Trace exporter
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		// lab: sample 100%
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	// Metric exporter
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp metric: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(10*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)

	// Log exporter
	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp log: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)

	// W3C traceparent + baggage propagation. payment-svc's otelhttp transport
	// injects both on /notify; without extraction here the trace from the
	// payment flow would die at this HTTP boundary and the baggage fields
	// (user.id, order.id) would never reach the logs.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	loggerProvider = lp
	meterProvider = mp

	// Shutdown flushes and closes every provider.
	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var firstErr error
		if err := tp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := mp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := lp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}, nil
}

// baggageFieldMap maps a baggage key to its log field. Listing them here keeps
// arbitrary baggage entries out of the logs.
var baggageFieldMap = map[string]string{
	"user.id":    "user_id",
	"session.id": "session_id",
	"order.id":   "order_id",
	"tenant.id":  "tenant_id",
}

// newLogger builds the fanout slog logger: JSON to stdout — which is what the
// checkout test greps from the pod log — and the same records over OTLP for
// Loki. Handlers inside a request must log with *Context(r.Context()): that is
// where the span and the baggage live, and the plain variants would leave the
// OTLP branch without trace_id or order_id.
func newLogger() *slog.Logger {
	// Stdout JSON, readable locally and scrapeable from the container log.
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	// OTel log bridge, shipping over OTLP.
	otelHandler := otelslog.NewHandler(serviceShortName,
		otelslog.WithLoggerProvider(loggerProvider),
	)

	h := &fanoutHandler{
		handlers: []slog.Handler{
			// Stdout gets trace_id/span_id injected here.
			&traceContextHandler{inner: stdoutHandler, withTraceIDs: true},
			// otelslog already carries trace_id and span_id on the records it
			// exports; injecting them again would produce two fields with the
			// same name and an ambiguous log line.
			&traceContextHandler{inner: otelHandler, withTraceIDs: false},
		},
	}

	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger
}

// fanoutHandler sends each record to every handler.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}

// traceContextHandler adds trace_id and span_id to every stdout record.
type traceContextHandler struct {
	inner slog.Handler
	// Set for handlers that do not resolve the span context themselves.
	// Duplicating trace_id onto a record that already has it produces two
	// fields with the same name and an ambiguous log line.
	withTraceIDs bool
}

func (t *traceContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return t.inner.Enabled(ctx, lvl)
}

func (t *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); t.withTraceIDs && sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	// Business ids from baggage: user_id, session_id, order_id, tenant_id.
	// otelslog carries trace context onto the OTLP records by itself, but
	// nothing puts these fields there — without this the logs shipped to Loki
	// would hold only what each call site happened to pass, while stdout
	// looked complete. Searching logs by order id is how an incident normally
	// starts, and it was quietly impossible.
	if bag := baggage.FromContext(ctx); bag.Len() > 0 {
		for _, m := range bag.Members() {
			if fieldName, ok := baggageFieldMap[m.Key()]; ok {
				r.AddAttrs(slog.String(fieldName, m.Value()))
			}
		}
	}

	return t.inner.Handle(ctx, r)
}

func (t *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: t.inner.WithAttrs(attrs), withTraceIDs: t.withTraceIDs}
}

func (t *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: t.inner.WithGroup(name), withTraceIDs: t.withTraceIDs}
}

// chaos holds the injected failure behaviour. Both values are read on every
// request, so changes take effect immediately.
type chaos struct {
	errorRate atomic.Uint64 // float64 bits, 0.0–1.0
	latencyMS atomic.Int64
}

func (c *chaos) rate() float64     { return math.Float64frombits(c.errorRate.Load()) }
func (c *chaos) setRate(v float64) { c.errorRate.Store(math.Float64bits(v)) }
func (c *chaos) latency() time.Duration {
	return time.Duration(c.latencyMS.Load()) * time.Millisecond
}

// notifyRequest is the payload payment-svc sends to /notify and /notify-failed.
type notifyRequest struct {
	OrderID   int     `json:"order_id"`
	PaymentID int     `json:"payment_id"`
	Amount    float64 `json:"amount"`
	UserID    int     `json:"user_id"`
}

// notification is one stored event. GET /notifications exists so the checkout
// test can prove payment-svc really called — the record is the proof.
type notification struct {
	ReceivedAt time.Time `json:"received_at"`
	Status     string    `json:"status"` // "received" | "failed"
	OrderID    int       `json:"order_id"`
	PaymentID  int       `json:"payment_id"`
	Amount     float64   `json:"amount"`
	UserID     int       `json:"user_id"`
}

// maxNotifications caps the store: nothing ever drains it, so without a cap a
// soak grows the slice until the pod goes with it.
const maxNotifications = 100

// notifyStore is the in-memory record of what was received. The mutex matters:
// /notify and /notify-failed write while /notifications reads, and a torn read
// would show up as a half-written record in the checkout proof.
type notifyStore struct {
	mu      sync.Mutex
	records []notification
}

func (s *notifyStore) add(n notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= maxNotifications {
		s.records = s.records[1:]
	}
	s.records = append(s.records, n)
}

// snapshot copies under the lock — returning the slice itself would let a
// later add race with the JSON encoder reading it.
func (s *notifyStore) snapshot() []notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]notification, len(s.records))
	copy(out, s.records)
	return out
}

func main() {
	port := env("PORT", "8080")

	otelShutdown, err := otelInit(context.Background())
	if err != nil {
		slog.Error("otel init failed", "err", err)
		os.Exit(1)
	}
	log := newLogger()

	c := &chaos{}
	notes := &notifyStore{}

	// otelhttp spans every request, extracts traceparent + baggage from
	// payment-svc's call, and records http.server metrics on the meter above —
	// the same instrument the services emit, so notification shows up in the
	// http_server_request_duration_seconds queries like every other hop.
	handler := otelhttp.NewHandler(
		newMux(log, c, notes),
		serviceShortName,
		otelhttp.WithMeterProvider(meterProvider),
	)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown matters here: during a canary step pods are terminated
	// constantly, and connections cut mid-flight would show up as errors the
	// analysis would blame on the new version.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", srv.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "err", err)
	}
	// Flush the batch exporters — a pod cut without this loses the last five
	// seconds of spans and, on /notify-failed, the very burst Loki is fed from.
	if err := otelShutdown(context.Background()); err != nil {
		log.Error("otel shutdown failed", "err", err)
	}
}

// newMux wires every route on its own mux instead of inside main so tests can
// drive the exact mux the process serves — a hand-rebuilt copy in a test would
// drift from this one and quietly stop testing what ships. The otelhttp wrap
// deliberately lives in main: tests run the mux bare, with no providers set,
// and otelhttp would then span and propagate nothing.
func newMux(log *slog.Logger, c *chaos, notes *notifyStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.Handle("GET /", instrument(c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "notification",
			"version": version,
			"path":    r.URL.Path,
		})
	})))

	mux.Handle("POST /notify", instrument(c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeNotify(w, r)
		if !ok {
			return
		}

		log.InfoContext(r.Context(), "notification received",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
			"amount", req.Amount,
			"user_id", req.UserID,
		)
		notes.add(notification{
			ReceivedAt: time.Now().UTC(),
			Status:     "received",
			OrderID:    req.OrderID,
			PaymentID:  req.PaymentID,
			Amount:     req.Amount,
			UserID:     req.UserID,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "received",
			"order_id":   req.OrderID,
			"payment_id": req.PaymentID,
		})
	})))

	mux.Handle("POST /notify-failed", instrument(c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeNotify(w, r)
		if !ok {
			return
		}

		// A burst, not one line: the point of this endpoint is a failure that
		// is visible in Loki, and a single error line is easy to miss in a
		// stream — five in a row is not.
		log.ErrorContext(r.Context(), "notification delivery failed",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
			"amount", req.Amount,
			"user_id", req.UserID,
		)
		log.ErrorContext(r.Context(), "provider unavailable",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
		)
		log.ErrorContext(r.Context(), "retry 1/3 exhausted",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
		)
		log.ErrorContext(r.Context(), "retry 2/3 exhausted",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
		)
		log.ErrorContext(r.Context(), "giving up: notification dropped",
			"order_id", req.OrderID,
			"payment_id", req.PaymentID,
		)
		notes.add(notification{
			ReceivedAt: time.Now().UTC(),
			Status:     "failed",
			OrderID:    req.OrderID,
			PaymentID:  req.PaymentID,
			Amount:     req.Amount,
			UserID:     req.UserID,
		})
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":      "notification delivery failed",
			"code":       "delivery_failed",
			"retryable":  false,
			"order_id":   req.OrderID,
			"payment_id": req.PaymentID,
		})
	})))

	mux.Handle("GET /notifications", instrument(c, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ns := notes.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"count":         len(ns),
			"notifications": ns,
		})
	})))

	mux.HandleFunc("GET /chaos", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"error_rate": c.rate(),
			"latency_ms": c.latencyMS.Load(),
		})
	})

	mux.HandleFunc("POST /chaos/error-rate", func(w http.ResponseWriter, r *http.Request) {
		v, err := readValue(r, "rate")
		if err != nil || v < 0 || v > 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "rate must be a number between 0 and 1",
				"usage": `curl -XPOST /chaos/error-rate -d '{"rate":0.5}'`,
			})
			return
		}
		c.setRate(v)
		log.InfoContext(r.Context(), "chaos updated", "error_rate", v)
		writeJSON(w, http.StatusOK, map[string]any{"error_rate": v})
	})

	mux.HandleFunc("POST /chaos/latency", func(w http.ResponseWriter, r *http.Request) {
		v, err := readValue(r, "ms")
		if err != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "ms must be a non-negative number",
				"usage": `curl -XPOST /chaos/latency -d '{"ms":800}'`,
			})
			return
		}
		c.latencyMS.Store(int64(v))
		log.InfoContext(r.Context(), "chaos updated", "latency_ms", int64(v))
		writeJSON(w, http.StatusOK, map[string]any{"latency_ms": int64(v)})
	})

	return mux
}

// instrument applies the injected chaos. Latency is added before the handler
// runs and the error is returned instead of calling it, so an injected failure
// looks like a genuinely broken build to anything watching. It wraps the data
// endpoints too: chaos injection is a second way to make a notification fail.
// /chaos/* and the probes stay unwrapped — failing the control plane would
// leave no way to turn the failure off. Request counts and durations are not
// recorded here any more: otelhttp already emits http.server metrics over
// OTLP, and nothing scrapes a private prometheus registry — the counters were
// write-only telemetry.
func instrument(c *chaos, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d := c.latency(); d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}

		if rate := c.rate(); rate > 0 && rand.Float64() < rate {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error":   "injected failure",
				"version": version,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// readValue accepts either a JSON body {"<key>": n} or a query parameter
// ?<key>=n, so the endpoints are usable from curl without quoting JSON.
func readValue(r *http.Request, key string) (float64, error) {
	if q := r.URL.Query().Get(key); q != "" {
		return strconv.ParseFloat(q, 64)
	}
	var body map[string]float64
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return 0, err
	}
	v, ok := body[key]
	if !ok {
		return 0, errors.New("missing key " + key)
	}
	return v, nil
}

// decodeNotify reads the shared /notify payload. order_id and payment_id are
// required — a record that cannot be tied back to a payment proves nothing in
// the checkout test, so a bad payload is rejected with a hint instead of
// stored half-empty.
func decodeNotify(w http.ResponseWriter, r *http.Request) (notifyRequest, bool) {
	var req notifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
			"usage": `curl -XPOST /notify -d '{"order_id":1,"payment_id":2,"amount":19.99,"user_id":3}'`,
		})
		return notifyRequest{}, false
	}
	if req.OrderID == 0 || req.PaymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "order_id and payment_id are required",
			"usage": `curl -XPOST /notify -d '{"order_id":1,"payment_id":2,"amount":19.99,"user_id":3}'`,
		})
		return notifyRequest{}, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// mustEnv fails the process at start rather than at first use — a pod started
// without its OTLP endpoint would otherwise run blind while looking healthy.
func mustEnv(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	panic(fmt.Sprintf("required env %q is not set", key))
}
