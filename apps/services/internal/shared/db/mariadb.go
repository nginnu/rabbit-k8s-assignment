// Package db helper เปิด connection ไปยัง MariaDB + Redis
// พร้อม OTel instrumentation ให้ทุก query สร้าง span อัตโนมัติ
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

// OpenMariaDB เชื่อมกับ MariaDB ด้วย gorm + otelgorm plugin
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

// OpenRedis เชื่อมกับ Redis + OTel instrumentation
//
// Redis เป็น non-critical dependency: auth เก็บ session (write-only ไม่เคยอ่าน,
// JWT verify ด้วย signature ล้วน) และ order ใช้เป็น cache-aside ที่ fall through
// ไป MariaDB ได้เอง. เพราะงั้น "ping ไม่ผ่าน" **ไม่ใช่เหตุผลที่จะไม่ start**.
//
// คืน client ที่ใช้งานได้เสมอ — go-redis reconnect ให้เองเมื่อ Redis กลับมา.
// error ที่คืนมาเป็น "เตือน" ไม่ใช่ "ตาย": caller ควร log แล้วเดินต่อ.
// ถ้าวันหนึ่งมี token revocation / logout (อ่าน session บน hot path)
// Redis จะกลายเป็น critical จริง แล้วต้องกลับมาทบทวนตรงนี้.
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

// PingRedis ตรวจว่า Redis ตอบไหม — แยกออกจาก OpenRedis เพื่อให้ caller
// เลือกได้ว่าจะ "เตือนแล้วไปต่อ" (non-critical) หรือ "ตาย" (critical).
// ทุก service วันนี้เลือกอย่างแรก.
func PingRedis(client *redis.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}
