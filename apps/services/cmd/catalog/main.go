// Package main — catalog entry point: the product list, split out of
// order-svc so the read path (browse) and the write path (order, settle) scale
// and fail independently. Listens on APP_PORT (default 9004), graceful
// shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/handler"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/repository"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/db"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/logger"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/profiler"
)

// serviceShortName must match the Deployment name in
// charts/apps/catalog/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error.
const serviceShortName = "catalog"

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

	// Redis is not critical here: ListProducts is cache-aside and falls through to
	// MariaDB on error, and the write-back is best effort. Losing Redis is slower,
	// not broken, so this must not exit.
	redisClient, err := db.OpenRedis(cfg)
	if err != nil {
		// A failure here is a bug in our instrumentation, not a dead Redis.
		log.Error("redis client init failed", "err", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	if err := db.PingRedis(redisClient); err != nil {
		log.Warn("redis unreachable at startup — continuing with DB-only reads",
			"err", err, "impact", "product list served from MariaDB; higher latency")
	}

	repo := repository.New(gormDB)
	uc := usecase.New(repo, usecase.NewRedisCache(redisClient))
	h := handler.New(uc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Health endpoint — BEFORE OTel → no trace for healthcheck noise
	r.GET("/healthz", handler.Healthz)

	// Business routes: full observability stack
	r.Use(middleware.OTel(cfg.ServiceName(serviceShortName)))
	r.Use(middleware.TraceResponseHeader())
	r.Use(middleware.BaggageToSpan()) // copy baggage (from upstream service) → span attr
	r.Use(middleware.RequestLogger())

	// GET /products carries no auth: the catalog is the storefront's landing
	// page, browsed before a visitor has signed in. JWT_SECRET stays a
	// required env below — config.LoadBase panics without it even though no
	// route reads it anymore, because the rest of the shared config depends
	// on the same LoadBase call succeeding.
	public := r.Group("/")
	{
		public.GET("/products", h.ListProducts)
	}

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "9004"
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
