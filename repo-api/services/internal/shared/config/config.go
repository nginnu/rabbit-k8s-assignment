// Package config holds the env vars every service shares. Read once at start;
// a missing one fails immediately rather than at first use.
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

	// FailRate is the fraction (0.0-1.0) of business-endpoint requests a service
	// should fail with 500, injected by middleware.FailInjector. Zero (the
	// default) means the flag costs nothing — no service is forced to set it,
	// and one left unset never calls the RNG. Used to prove an Argo Rollouts
	// canary rolls back on a build that starts and passes probes but serves
	// broken responses.
	FailRate float64
}

// ServiceName builds service.name as <prefix>-<env>-<name>. The observability
// pipeline parses this shape, so changing it breaks the dashboards.
func (b Base) ServiceName(shortName string) string {
	return fmt.Sprintf("%s-%s-%s", b.SvcPrefix, b.DeploymentEnv, shortName)
}

// LoadBase reads the shared env vars and panics on a missing one.
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

		FailRate: getFloatOr("FAIL_RATE", 0),
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

func getFloatOr(k string, def float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		panic(fmt.Sprintf("config: env %q must be float, got %q", k, v))
	}
	return n
}
