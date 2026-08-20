// Package main — payment: chaos-controllable external payment gateway
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/logger"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/profiler"
)

// serviceShortName must match the Deployment name in
// charts/apps/payment/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error (see notes/slo-strategy.md).
const serviceShortName = "payment"

func main() {
	ctx := context.Background()
	cfg := config.LoadBase()

	shutdown, err := sharedotel.Init(ctx, cfg, serviceShortName)
	if err != nil {
		slog.Error("otel init failed", "err", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	profiler.Init(cfg.ServiceName(serviceShortName))

	log := logger.Init(serviceShortName)
	serviceName := cfg.ServiceName(serviceShortName)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Health endpoint — BEFORE OTel → no trace for healthcheck noise
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Business routes: full observability stack
	r.Use(middleware.OTel(serviceName), middleware.TraceResponseHeader(), middleware.BaggageToSpan(), middleware.RequestLogger())

	tracer := otel.Tracer(serviceName)

	r.POST("/charge", func(c *gin.Context) {
		var body struct {
			Amount float64 `json:"amount" binding:"required,gt=0"`
			Method string  `json:"method" binding:"required,oneof=card truemoney gwallet cod"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			// The discarded error this replaces let a malformed body fall
			// through with Amount at its zero value: a 0-amount charge that
			// still answered 200 {"status":"ok"} — a payment "succeeding"
			// for an amount nobody actually charged. oneof on Method closes
			// the same gap for a channel this gateway does not recognize:
			// it fails here with a 400 body it can quote, not silently at
			// the DB's enum constraint two services upstream.
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Read chaos headers
		errRate, _ := strconv.ParseFloat(c.GetHeader("X-Chaos-Error-Rate"), 64)
		latency, _ := strconv.Atoi(c.GetHeader("X-Chaos-Latency-Ms"))
		errType := c.GetHeader("X-Chaos-Error-Type")
		if errType == "" {
			errType = "500"
		}

		ctx := c.Request.Context()
		ctx, span := tracer.Start(ctx, "mock.process", trace.WithAttributes(
			attribute.Float64("chaos.error_rate", errRate),
			attribute.Int("chaos.latency_ms", latency),
			attribute.String("chaos.error_type", errType),
			attribute.String("payment.method", body.Method),
		))
		defer span.End()

		log.InfoContext(ctx, "charge attempt", "amount", body.Amount, "method", body.Method)

		// cod is the one channel this gateway deliberately never settles.
		// It fails before the chaos dial is even read, on every call, with
		// no dependence on X-Chaos-* — the checkout proof needs a failure
		// that reproduces 100% of the time, not one gated behind a random
		// draw. The span carries codes.Error plus the method that declined,
		// so a Tempo search for a failed cod span does not depend on chaos
		// headers being set at all; the log line below carries the same two
		// facts for Loki.
		if body.Method == "cod" {
			const reason = "cod settlement is not supported by this gateway"
			span.SetStatus(codes.Error, reason)
			span.SetAttributes(attribute.String("payment.decline_reason", reason))
			log.ErrorContext(ctx, "charge declined", "method", body.Method, "reason", reason)
			c.JSON(http.StatusPaymentRequired, gin.H{"error": reason})
			return
		}

		// Apply base latency
		if latency > 0 {
			time.Sleep(time.Duration(latency) * time.Millisecond)
		}

		// Chaos trigger
		if errRate > 0 && rand.Float64() < errRate {
			span.AddEvent("chaos.triggered", trace.WithAttributes(
				attribute.String("error_type", errType),
			))
			span.SetStatus(codes.Error, "chaos: "+errType)

			switch errType {
			case "timeout":
				time.Sleep(5 * time.Second)
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "gateway timeout"})
			case "connection_reset":
				// Hijack and close the raw TCP connection
				hj, ok := c.Writer.(http.Hijacker)
				if ok {
					conn, _, _ := hj.Hijack()
					if conn != nil {
						_ = conn.(*net.TCPConn).SetLinger(0)
						conn.Close()
					}
				}
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway declined"})
			}
			return
		}

		ref := fmt.Sprintf("pay-%s", uuid.New().String())
		c.JSON(http.StatusOK, gin.H{"status": "ok", "ref": ref})
	})

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "7000"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		log.Info("service ready", "service", serviceName, "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// r.Run() blocked here previously, so a rollout's SIGTERM killed the
	// process mid in-flight charge with no chance to finish it — the same gap
	// every other service in this repo closes with a graceful Shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", "err", err)
	}
}
