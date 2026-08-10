// Package config รวม env vars ที่ทุก service ใช้ร่วม
// อ่านครั้งเดียวตอน service start — ถ้าขาด fail fast
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Base struct {
	DeploymentEnv string // local | qa | dev | prod
	SvcPrefix     string // "platform"
	SvcVersion    string // "0.1.0"

	OTLPEndpoint string // e.g. http://otel-collector:4317
	OTLPProtocol string // grpc | http/protobuf

	MariaDBDSN string // user:pass@tcp(host:3306)/db?parseTime=true
	RedisAddr  string // host:6379

	JWTSecret      string
	JWTExpireHours int
}

// ServiceName คำนวณ service.name ตาม pattern: <prefix>-<env>-<name>
func (b Base) ServiceName(shortName string) string {
	return fmt.Sprintf("%s-%s-%s", b.SvcPrefix, b.DeploymentEnv, shortName)
}

// LoadBase อ่าน env vars ที่ share กัน ถ้าขาด panic
func LoadBase() Base {
	return Base{
		DeploymentEnv: mustGet("DEPLOYMENT_ENV"),
		SvcPrefix:     getOr("SVC_PREFIX", "platform"),
		SvcVersion:    getOr("SVC_VERSION", "0.0.0"),

		OTLPEndpoint: mustGet("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OTLPProtocol: getOr("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),

		MariaDBDSN: mustGet("MARIADB_DSN"),
		RedisAddr:  getOr("REDIS_ADDR", "redis:6379"),

		JWTSecret:      mustGet("JWT_SECRET"),
		JWTExpireHours: getIntOr("JWT_EXPIRE_HOURS", 1),
	}
}

func mustGet(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic(fmt.Sprintf("config: required env %q is not set", k))
	}
	return v
}

func getOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getIntOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("config: env %q must be int, got %q", k, v))
	}
	return n
}
