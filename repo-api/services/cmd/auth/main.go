// Package main — auth: authentication service with JWT + Redis session.
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

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/handler"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/repository"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/db"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/logger"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/profiler"
)

// serviceShortName must match the Deployment name in
// charts/apps/auth/values.yaml — it feeds cfg.ServiceName(), which
// becomes the OTel service.name resource attribute and, after
// resource_to_telemetry_conversion in Alloy, the service_name label that SLO
// alert queries match on. A mismatch here makes those queries return zero
// series with no error (see notes/slo-strategy.md).
const serviceShortName = "auth"

func main() {
	ctx := context.Background()
	cfg := config.LoadBase()

	// OTel SDK (tracer, meter, logger provider)
	shutdown, err := sharedotel.Init(ctx, cfg, serviceShortName)
	if err != nil {
		slog.Error("otel init failed", "err", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	// Continuous profiler (Pyroscope)
	profiler.Init(cfg.ServiceName(serviceShortName))

	// Structured logger with trace_id injection
	log := logger.Init(serviceShortName)
	log.Info("starting service", "service", cfg.ServiceName(serviceShortName))

	// MariaDB
	gormDB, err := db.OpenMariaDB(cfg)
	if err != nil {
		log.Error("mariadb open failed", "err", err)
		os.Exit(1)
	}

	// Redis is not critical here: sessions are written but never read back, and
	// JWTs verify by signature and expiry alone. Losing Redis leaves login fully
	// working, so this must not exit.
	redisClient, err := db.OpenRedis(cfg)
	if err != nil {
		// A failure here is a bug in our instrumentation, not a dead Redis.
		log.Error("redis client init failed", "err", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	if err := db.PingRedis(redisClient); err != nil {
		log.Warn("redis unreachable at startup — continuing without session store",
			"err", err, "impact", "sessions not persisted; login unaffected")
	}

	// Repositories
	userRepo := repository.NewGormUserRepo(gormDB)
	sessionRepo := repository.NewRedisSessionRepo(redisClient)

	// Use cases
	loginUC := usecase.NewLoginUsecase(userRepo, sessionRepo, cfg.JWTSecret, cfg.JWTExpireHours)

	// Handler
	authHandler := handler.NewAuthHandler(loginUC)

	// Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Health endpoint — registered BEFORE OTel so healthcheck probes
	// Not traced: every 10s across four services is ~100 traces a minute of noise.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Business routes: full observability stack
	r.Use(middleware.OTel(cfg.ServiceName(serviceShortName)))
	r.Use(middleware.TraceResponseHeader())
	r.Use(middleware.BaggageToSpan()) // copy baggage (from upstream service) → span attr
	r.Use(middleware.RequestLogger())

	r.POST("/auth/login", authHandler.Login)

	// HTTP server
	port := getOr("APP_PORT", "9001")
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		log.Info("service ready", "addr", srv.Addr, "service", cfg.ServiceName(serviceShortName))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		log.Error("server error", "err", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", "err", err)
	}
	log.Info("service stopped")
}

func getOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
