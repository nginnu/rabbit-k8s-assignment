// Package middleware holds the gin middleware every service shares.
package middleware

import (
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// OTel wraps otelgin. Register it before TraceResponseHeader, which reads the
// span it puts on the request context.
func OTel(serviceName string) gin.HandlerFunc {
	return otelgin.Middleware(serviceName)
}

// TraceResponseHeader returns the trace id to the caller as
// `traceresponse: 00-<trace_id>-<span_id>-01`, so a request can be looked up in
// Grafana afterwards.
//
// The header is set before c.Next(), not after: once the handler writes the body
// the headers are already flushed and a later Set is silently lost.
func TraceResponseHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if sc := trace.SpanContextFromContext(c.Request.Context()); sc.IsValid() {
			c.Writer.Header().Set("traceresponse",
				"00-"+sc.TraceID().String()+"-"+sc.SpanID().String()+"-01")
		}
		c.Next()
	}
}

// RequestLogger logs a one-line summary once the handler has returned.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.InfoContext(c.Request.Context(), "http request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// FailInjector fails the given fraction of requests with 500, to prove a
// canary rollback works against a build that starts cleanly and passes
// /healthz but serves broken responses — a crashing pod never reaches
// canary analysis because readiness catches it first, so it proves nothing.
//
// Register it AFTER OTel(): otelgin reads c.Writer.Status() once c.Next()
// returns, so the 500 written here by AbortWithStatus is what the metric and
// span record, the same way Auth() already short-circuits inside the traced
// chain with AbortWithStatusJSON.
//
// rate <= 0 (the default — FAIL_RATE unset) returns a plain passthrough, not
// a wrapped no-op check, so a service that never sets FAIL_RATE pays no
// per-request cost, not even the rand.Float64 call.
func FailInjector(rate float64) gin.HandlerFunc {
	if rate <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if rand.Float64() < rate {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Next()
	}
}

// CORS allows all origins — lab only, do not use in production
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Chaos-Error-Rate, X-Chaos-Latency-Ms, X-Chaos-Error-Type, traceparent")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "traceresponse")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// JWTClaims is the user identity placed on the request context.
type JWTClaims struct {
	UserID    int    `json:"uid"`
	Username  string `json:"usr"`
	SessionID string `json:"sid"` // browser/device session — stable across token refresh
	jwt.RegisteredClaims
}

const ctxUserKey = "user"

// Auth verifies the JWT from the Authorization header.
//
// It also puts user.id and session.id into baggage, which propagates to every
// downstream service without them having to read the token again.
func Auth(cfg config.Base) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		raw = strings.TrimPrefix(raw, "Bearer ")
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims := &JWTClaims{}
		// WithValidMethods pins parsing to HS256: without it, a token whose
		// header claims "alg":"none" (or any other method) still reaches the
		// keyfunc, which returns the same HMAC secret regardless — the classic
		// jwt alg-confusion hole, closed here rather than trusted to every
		// caller minting a token to always pick HS256 on its own.
		token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(cfg.JWTSecret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}))
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ctxUserKey, claims)

		// Set on the current span directly rather than waiting for BaggageToSpan.
		span := trace.SpanFromContext(c.Request.Context())
		span.SetAttributes(attribute.Int("user.id", claims.UserID))
		if claims.SessionID != "" {
			span.SetAttributes(attribute.String("session.id", claims.SessionID))
		}

		// Baggage carries these to the next service via otelhttp injection.
		ctx := c.Request.Context()
		members := []baggage.Member{}
		if m, err := baggage.NewMember("user.id", strconv.Itoa(claims.UserID)); err == nil {
			members = append(members, m)
		}
		if claims.SessionID != "" {
			if m, err := baggage.NewMember("session.id", claims.SessionID); err == nil {
				members = append(members, m)
			}
		}
		if len(members) > 0 {
			if bag, err := baggage.New(members...); err == nil {
				ctx = baggage.ContextWithBaggage(ctx, bag)
				c.Request = c.Request.WithContext(ctx)
			}
		}

		c.Next()
	}
}

// BaggageToSpan copy baggage members → span attributes (current span)
//
// Copies baggage onto the span so Tempo can be queried by it, for example
// { span.user.id = "2" }. Register after OTel and after Auth.
func BaggageToSpan() gin.HandlerFunc {
	return func(c *gin.Context) {
		span := trace.SpanFromContext(c.Request.Context())
		bag := baggage.FromContext(c.Request.Context())
		for _, m := range bag.Members() {
			span.SetAttributes(attribute.String(m.Key(), m.Value()))
		}
		c.Next()
	}
}

// UserFromContext returns the claims Auth stored.
func UserFromContext(c *gin.Context) (*JWTClaims, bool) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*JWTClaims)
	return claims, ok
}
