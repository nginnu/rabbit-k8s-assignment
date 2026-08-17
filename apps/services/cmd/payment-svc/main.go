// Package main — payment-svc: deepest trace in the lab (10+ spans).
// Flow: JWT verify -> validate order -> create pending payment -> charge payment-gateway -> update payment -> mark order paid -> notify notification.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/handler"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/repository"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/db"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/logger"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/profiler"

	"github.com/gin-gonic/gin"
)

// serviceShortName must match the Deployment name in
// charts/apps/payment-svc/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error (see notes/slo-strategy.md).
const serviceShortName = "payment-svc"

func main() {
	ctx := context.Background()
	cfg := config.LoadBase()

	// OTel SDK init
	shutdown, err := sharedotel.Init(ctx, cfg, serviceShortName)
	if err != nil {
		slog.Error("otel init failed", "err", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	profiler.Init(cfg.ServiceName(serviceShortName))

	// Structured logger with trace_id injection
	log := logger.Init(serviceShortName)
	log.Info("starting service", "service", cfg.ServiceName(serviceShortName))

	// MariaDB
	gormDB, err := db.OpenMariaDB(cfg)
	if err != nil {
		log.Error("mariadb connect failed", "err", err)
		os.Exit(1)
	}

	// Service URLs from env
	orderSvcURL := mustEnv("ORDER_SVC_URL")
	gatewayURL := mustEnv("PAYMENT_GATEWAY_URL")
	notifURL := mustEnv("NOTIFICATION_URL")

	// Wire dependencies
	paymentRepo := repository.New(gormDB)
	orderClient := gateway.NewOrderClient(orderSvcURL)
	gatewayClient := gateway.NewPaymentGatewayClient(gatewayURL)
	notificationClient := gateway.NewNotificationClient(notifURL)
	paymentUC := usecase.New(paymentRepo, orderClient, gatewayClient, notificationClient)
	paymentHandler := handler.New(paymentUC)

	// Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Health endpoint — BEFORE OTel → no trace for healthcheck noise
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": serviceShortName})
	})

	// Business routes: full observability stack
	r.Use(middleware.OTel(cfg.ServiceName(serviceShortName)))
	r.Use(middleware.TraceResponseHeader())
	r.Use(middleware.BaggageToSpan()) // copy baggage (from upstream service) → span attr
	r.Use(middleware.RequestLogger())

	// Payment endpoint (requires JWT)
	authorized := r.Group("/")
	authorized.Use(middleware.Auth(cfg))
	authorized.POST("/payments", paymentHandler.CreatePayment)
	// Confirmation step: gives the purchase flow a visible end, so a completed
	// journey is provable rather than inferred from the absence of an error.
	authorized.GET("/payments/:id/success", paymentHandler.PaymentSuccess)

	// HTTP server with graceful shutdown. APP_PORT, not PORT: every sibling
	// service (order-svc, catalog, payment-gateway) reads APP_PORT, and
	// this was the one holdout reading a different variable for the same
	// setting.
	port := envOr("APP_PORT", "9003")
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		log.Info("listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env %q is not set", key))
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
