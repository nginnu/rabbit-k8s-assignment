// Package middleware รวม gin middleware ที่ทุก service ใช้ร่วม
//   - OTel:    otelgin auto trace + metric
//   - Request: log request summary พร้อม trace_id
//   - Auth:    JWT verify (optional — ใช้เฉพาะ endpoint ที่ต้องการ)
//   - CORS:    allow all (lab only)
package middleware

import (
	"log/slog"
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

// OTel builds gin middleware สำหรับ tracing (otelgin wrapper).
// ใช้คู่กับ TraceResponseHeader() เพื่อ propagate trace_id กลับไปยัง client
// ลำดับที่ต้องใช้:
//
//	r.Use(middleware.OTel(serviceName))
//	r.Use(middleware.TraceResponseHeader())   // ← หลัง OTel
//	r.Use(middleware.RequestLogger())
func OTel(serviceName string) gin.HandlerFunc {
	return otelgin.Middleware(serviceName)
}

// TraceResponseHeader ใส่ header `traceresponse: 00-<trace_id>-<span_id>-01`
// ลงใน response เพื่อให้ client (Playwright / manual test / browser) copy ไปใช้
// query ใน Grafana ได้.
//
// หมายเหตุสำคัญ — middleware นี้ต้องรัน HERE (ก่อน c.Next()) ไม่ใช่หลัง เพราะ
// response header ต้องตั้งก่อน handler เขียน body (ไม่งั้น flush แล้ว header ไม่ติด)
//
// ลำดับที่ทำให้ทำงานได้:
//  1. OTel() → otelgin ตั้ง span ใน c.Request.Context() แล้ว call c.Next()
//  2. TraceResponseHeader() ทำงาน: อ่าน span จาก ctx → set header → c.Next()
//  3. Handler ทำงาน → c.JSON() เขียน response (header ถูก flush พร้อม body)
func TraceResponseHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if sc := trace.SpanContextFromContext(c.Request.Context()); sc.IsValid() {
			c.Writer.Header().Set("traceresponse",
				"00-"+sc.TraceID().String()+"-"+sc.SpanID().String()+"-01")
		}
		c.Next()
	}
}

// RequestLogger logs request summary หลัง handler ทำงานเสร็จ
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

// JWTClaims: user identity ที่ถูกใส่ลง context
type JWTClaims struct {
	UserID    int    `json:"uid"`
	Username  string `json:"usr"`
	SessionID string `json:"sid"` // browser/device session — stable across token refresh
	jwt.RegisteredClaims
}

const ctxUserKey = "user"

// Auth ตรวจ JWT จาก Authorization: Bearer <token>
//
// นอกจาก verify token, ยัง:
//  - ใส่ claims ลง gin context (UserFromContext ใช้ต่อ)
//  - ใส่ user.id + session.id ลง baggage → propagate ไป downstream service อัตโนมัติ
func Auth(cfg config.Base) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		raw = strings.TrimPrefix(raw, "Bearer ")
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims := &JWTClaims{}
		token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ctxUserKey, claims)

		// Set บน span ปัจจุบันทันที (ไม่ต้องรอ BaggageToSpan middleware)
		span := trace.SpanFromContext(c.Request.Context())
		span.SetAttributes(attribute.Int("user.id", claims.UserID))
		if claims.SessionID != "" {
			span.SetAttributes(attribute.String("session.id", claims.SessionID))
		}

		// + baggage → propagate downstream (otelhttp injection ต่อไปยัง service อื่น)
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
// ใช้คู่กับ Auth() — baggage propagate อัตโนมัติข้าม service,
// middleware นี้ทำให้ทุก service เห็น user.id / session.id / order.id เป็น span attribute
// → Tempo query ได้: { span.user.id = "2" }
//
// ต้อง register หลัง OTel middleware + หลัง Auth (ถ้ามี)
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

// UserFromContext ดึง claims ที่ Auth ใส่ไว้
func UserFromContext(c *gin.Context) (*JWTClaims, bool) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*JWTClaims)
	return claims, ok
}
