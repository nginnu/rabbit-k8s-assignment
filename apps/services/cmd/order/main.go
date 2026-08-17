// Package main — order entry point. Owns both /orders and /payments: payment
// was never its own bounded data owner, it validated an order, read its
// price, and flipped it to paid — all three go through order's usecase, so
// the two now share this process, this DB connection, and this deploy.
// Listens on APP_PORT (default 9002), graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	orderhandler "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/handler"
	orderrepository "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/repository"
	orderusecase "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/usecase"
	paymentgateway "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"
	paymenthandler "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/handler"
	paymentrepository "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/repository"
	paymentusecase "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/db"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/logger"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/profiler"
)

// serviceShortName must match the Deployment name in
// charts/apps/order/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error (see notes/slo-strategy.md).
const serviceShortName = "order"

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

	gormDB, err := db.OpenMariaDB(cfg)
	if err != nil {
		log.Error("mariadb connection failed", "err", err)
		os.Exit(1)
	}

	// No Redis since the catalog split: the product cache-aside that used it
	// moved to catalog, and nothing else in the order lifecycle caches.
	orderRepo := orderrepository.New(gormDB)
	orderUC := orderusecase.New(orderRepo)
	orderH := orderhandler.New(orderUC)

	// Payment settlement, merged in from payment-svc. Validate/MarkPaid were
	// the only two calls payment made across the network to order-svc; both
	// become direct calls into orderUC through this adapter now that the two
	// share a process — see internal/payment/gateway/order_adapter.go.
	paymentRepo := paymentrepository.New(gormDB)
	orderAdapter := paymentgateway.NewOrderAdapter(orderUC)
	gatewayClient := paymentgateway.NewPaymentGatewayClient(mustEnv("PAYMENT_GATEWAY_URL"))
	notificationClient := paymentgateway.NewNotificationClient(mustEnv("NOTIFICATION_URL"))
	paymentUC := paymentusecase.New(paymentRepo, orderAdapter, gatewayClient, notificationClient)
	paymentH := paymenthandler.New(paymentUC)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Health endpoint — BEFORE OTel → no trace for healthcheck noise
	r.GET("/healthz", orderhandler.Healthz)

	// Business routes: full observability stack
	r.Use(middleware.OTel(cfg.ServiceName(serviceShortName)))
	r.Use(middleware.TraceResponseHeader())
	r.Use(middleware.BaggageToSpan()) // copy baggage (from upstream service) → span attr
	r.Use(middleware.RequestLogger())

	// Order routes (JWT required, FailInjector applies)
	orders := r.Group("/")
	// FailInjector is scoped to this group, not r.Use() at the root: the
	// canary demo it exists for proves a rollback on the order rollout, and
	// the load generator behind it (tests/order-svc-canary-load.js) only
	// exercises /api/orders. It still comes after OTel: otelgin records
	// status from c.Writer.Status() once c.Next() returns, so a 500 written
	// here still lands in http_server_request_duration_seconds_count with
	// that code.
	orders.Use(middleware.FailInjector(cfg.FailRate))
	orders.Use(middleware.Auth(cfg))
	{
		orders.POST("/orders", orderH.CreateOrder)
		orders.GET("/orders", orderH.ListOrders)
	}

	// Payment routes (JWT required, no FailInjector). payment-svc never
	// carried fault injection: settlement failing because of an injected 500
	// would read as a broken checkout, not the order rollout FailInjector
	// exists to catch. Keeping payments out of that group after the merge
	// preserves that distinction.
	payments := r.Group("/")
	payments.Use(middleware.Auth(cfg))
	{
		payments.POST("/payments", paymentH.CreatePayment)
		// Confirmation step: gives the purchase flow a visible end, so a
		// completed journey is provable rather than inferred from the
		// absence of an error.
		payments.GET("/payments/:id/success", paymentH.PaymentSuccess)
	}

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "9002"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		log.Info("service ready",
			"service", cfg.ServiceName(serviceShortName),
			"port", port,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

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

// mustEnv panics on a missing var rather than starting half-configured: a
// service that comes up without PAYMENT_GATEWAY_URL or NOTIFICATION_URL
// would answer /healthz OK and only fail once the first checkout hit it.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env %q is not set", key))
	}
	return v
}
