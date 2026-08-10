// Package main — mock-payment: chaos-controllable external payment gateway
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
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
// charts/apps/mock-payment/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error (see notes/slo-strategy.md).
const serviceShortName = "mock-payment"

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
	r.Use(middleware.CORS())

	// Health endpoint — BEFORE OTel → no trace for healthcheck noise
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Business routes: full observability stack
	r.Use(middleware.OTel(serviceName), middleware.TraceResponseHeader(), middleware.BaggageToSpan(), middleware.RequestLogger())

	tracer := otel.Tracer(serviceName)

	r.POST("/charge", func(c *gin.Context) {
		// Parse body (don't validate — it's a mock)
		var body struct{ Amount float64 }
		_ = c.ShouldBindJSON(&body)

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
		))
		defer span.End()

		log.InfoContext(ctx, "charge attempt", "amount", body.Amount)

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
	log.Info("service ready", "service", serviceName, "port", port)
	if err := r.Run(":" + port); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
