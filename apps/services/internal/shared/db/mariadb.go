// Package db opens the MariaDB and Redis connections, both instrumented so
// every query produces a span.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenMariaDB connects with gorm and the otelgorm plugin.
func OpenMariaDB(cfg config.Base) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.MariaDBDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	if err := db.Use(otelgorm.NewPlugin()); err != nil {
		return nil, fmt.Errorf("otelgorm: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

// OpenRedis connects to Redis with OTel instrumentation.
//
// Redis is not a critical dependency here: auth writes sessions but never reads
// them back (JWTs verify by signature alone), and order uses it as a cache-aside
// that falls through to MariaDB. A failed ping is therefore not a reason to
// refuse to start.
//
// The returned client is always usable — go-redis reconnects on its own. The
// error is a warning, not a fatal: log it and carry on. If token revocation or
// logout is added, sessions land on the hot path and this has to be revisited.
func OpenRedis(cfg config.Base) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisotel.InstrumentTracing(client); err != nil {
		return nil, fmt.Errorf("redisotel trace: %w", err)
	}
	if err := redisotel.InstrumentMetrics(client); err != nil {
		return nil, fmt.Errorf("redisotel metric: %w", err)
	}
	return client, nil
}

// PingRedis is separate from OpenRedis so the caller decides whether a dead
// Redis is a warning or fatal. Every service today treats it as a warning.
func PingRedis(client *redis.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}
