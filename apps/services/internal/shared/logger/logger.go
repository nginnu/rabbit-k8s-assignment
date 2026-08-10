// Package logger ตั้ง slog handler ที่:
//  1. ยิงผ่าน OTel log bridge ไป OTLP
//  2. inject trace_id / span_id จาก context อัตโนมัติ
//  3. print JSON ไป stdout ด้วย (developer ดู local ได้)
package logger

import (
	"context"
	"log/slog"
	"os"

	sharedotel "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/otel"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// baggageFieldMap: key ใน baggage → field name ใน log
// ลิสต์ไว้ที่นี่เพื่อจำกัด keys ที่จะ inject (ไม่ใช่ทุก baggage entry)
var baggageFieldMap = map[string]string{
	"user.id":    "user_id",
	"session.id": "session_id",
	"order.id":   "order_id",
	"tenant.id":  "tenant_id",
}

// Init ติดตั้ง default slog logger สำหรับ service
// serviceShortName ใช้เป็น logger name (เช่น "auth-svc")
func Init(serviceShortName string) *slog.Logger {
	// Stdout JSON handler — สำหรับ dev อ่าน + ถ้า alloy scrape container log ก็ได้
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	// OTel log bridge → ยิงไป OTLP
	otelHandler := otelslog.NewHandler(serviceShortName,
		otelslog.WithLoggerProvider(sharedotel.LoggerProvider()),
	)

	// Both handlers get the business-id wrapper. otelslog fills in trace_id and
	// span_id by itself, but nothing puts user_id, session_id or order_id on
	// the records it exports — so without this the logs shipped to Loki carry
	// only what each call site happened to pass, while stdout looked complete.
	// Searching logs by order id is the normal way an incident starts, and it
	// was silently impossible.
	h := &fanoutHandler{
		handlers: []slog.Handler{
			&traceContextHandler{inner: stdoutHandler, withTraceIDs: true},
			// otelslog already carries trace_id and span_id on the records it
			// exports, but nothing puts user_id, session_id or order_id there —
			// so the logs shipped to Loki held only what each call site happened
			// to pass, while stdout looked complete. Searching logs by order id
			// is how an incident normally starts, and it was quietly impossible.
			&traceContextHandler{inner: otelHandler, withTraceIDs: false},
		},
	}

	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger
}

// fanoutHandler ส่ง record ไปทุก handler
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}

// traceContextHandler เพิ่ม trace_id / span_id ลงทุก record ที่ผ่าน stdout
type traceContextHandler struct {
	inner slog.Handler
	// Set for handlers that do not resolve the span context themselves.
	// Duplicating trace_id onto a record that already has it produces two
	// fields with the same name and an ambiguous log line.
	withTraceIDs bool
}

func (t *traceContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return t.inner.Enabled(ctx, lvl)
}

func (t *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); t.withTraceIDs && sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	// Inject business IDs จาก baggage (user_id, session_id, order_id, tenant_id)
	if bag := baggage.FromContext(ctx); bag.Len() > 0 {
		for _, m := range bag.Members() {
			if fieldName, ok := baggageFieldMap[m.Key()]; ok {
				r.AddAttrs(slog.String(fieldName, m.Value()))
			}
		}
	}

	return t.inner.Handle(ctx, r)
}

func (t *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: t.inner.WithAttrs(attrs), withTraceIDs: t.withTraceIDs}
}

func (t *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: t.inner.WithGroup(name), withTraceIDs: t.withTraceIDs}
}
