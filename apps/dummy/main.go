// Command dummy is a deliberately controllable service used to prove the
// delivery pipeline end to end — in particular that a bad release is detected
// by canary analysis and rolled back automatically.
//
// A real service cannot be told to fail on demand, so it cannot prove
// auto-rollback. This one can: /chaos/error-rate and /chaos/latency change how
// it behaves at runtime, without a redeploy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by path, method and response code.",
	}, []string{"path", "method", "code"})

	duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method"})

	buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "app_build_info",
		Help: "Always 1; the version label carries the running build.",
	}, []string{"version"})
)

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

func main() {
	port := env("PORT", "8080")
	buildInfo.WithLabelValues(version).Set(1)

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	c := &chaos{}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.Handle("GET /", instrument(c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "dummy",
			"version": version,
			"path":    r.URL.Path,
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
		log.Info("chaos updated", "error_rate", v)
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
		log.Info("chaos updated", "latency_ms", int64(v))
		writeJSON(w, http.StatusOK, map[string]any{"latency_ms": int64(v)})
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
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
}

// instrument records metrics and applies the injected chaos. Latency is added
// before the handler runs and the error is returned instead of calling it, so
// an injected failure looks like a genuinely broken build to anything watching.
func instrument(c *chaos, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if d := c.latency(); d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}

		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		if rate := c.rate(); rate > 0 && rand.Float64() < rate {
			writeJSON(rec, http.StatusInternalServerError, map[string]string{
				"error":   "injected failure",
				"version": version,
			})
		} else {
			next.ServeHTTP(rec, r)
		}

		route := r.URL.Path
		if route != "/" {
			route = "other"
		}
		requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.code)).Inc()
		duration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
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
