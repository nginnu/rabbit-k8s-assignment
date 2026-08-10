// Package otel sets up the OpenTelemetry SDK — traces, metrics and logs over
// OTLP/gRPC. Call Init() from main and defer the returned shutdown.
package otel

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Shutdown is returned by Init; defer it in main.
type Shutdown func(context.Context) error

// Init registers the tracer, meter and logger providers globally. service.name
// is <prefix>-<env>-<svcShortName>.
//
// deployment.environment is deliberately not sent — Alloy derives it from
// service.name, so setting it here would produce two sources for one label.
func Init(ctx context.Context, cfg config.Base, svcShortName string) (Shutdown, error) {
	serviceName := cfg.ServiceName(svcShortName)

	hostname, _ := os.Hostname()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(cfg.SvcVersion),
			semconv.ServiceInstanceID(hostname),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// Trace exporter
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		// lab: sample 100%
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	// Metric exporter
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(cfg.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp metric: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(10*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)

	// Log exporter
	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(cfg.OTLPEndpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp log: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)

	// W3C traceparent + baggage propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Stored globally for the logger package.
	globalLoggerProvider = lp

	// Shutdown flushes and closes every provider.
	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var firstErr error
		if err := tp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := mp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := lp.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}, nil
}

// globalLoggerProvider is read by shared/logger.
var globalLoggerProvider *sdklog.LoggerProvider

// LoggerProvider exposes the provider to the logger package.
func LoggerProvider() *sdklog.LoggerProvider {
	return globalLoggerProvider
}
