package runtime

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTelConfig holds configuration for OpenTelemetry initialization.
type OTelConfig struct {
	ServiceName  string
	OTLPEndpoint string // empty = tracing disabled
}

// OTelShutdown is returned by InitOTel and should be called on shutdown.
type OTelShutdown func(ctx context.Context) error

// InitOTel initializes OpenTelemetry with trace propagation and Prometheus
// metrics. It sets global TracerProvider, MeterProvider, and TextMapPropagator.
//
// Trace propagation uses W3C TraceContext (traceparent/tracestate headers).
// If no traceparent arrives from upstream (e.g. nginx), a new root span is
// created automatically.
//
// Returns a shutdown function that flushes pending spans and stops providers.
func InitOTel(ctx context.Context, cfg OTelConfig) (OTelShutdown, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	// W3C trace propagation (traceparent + tracestate)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var shutdowns []func(context.Context) error

	// Trace exporter (OTLP over gRPC)
	if cfg.OTLPEndpoint != "" {
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}

	// Prometheus metrics exporter
	promExporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	shutdowns = append(shutdowns, mp.Shutdown)

	shutdown := func(ctx context.Context) error {
		var firstErr error
		for _, fn := range shutdowns {
			if err := fn(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	return shutdown, nil
}

// OTelMiddleware returns a chi-compatible middleware that:
//   - Extracts W3C traceparent from incoming request (or creates root span)
//   - Records HTTP request metrics (duration, status, method, route)
//   - Propagates trace context to downstream gRPC calls via otelgrpc
func OTelMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, serviceName)
	}
}

// MetricsHandler returns an HTTP handler that serves Prometheus metrics
// at /metrics. The OTel Prometheus exporter registered a global collector;
// promhttp.Handler() serves it.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// activeConnectionsCounter tracks concurrent WS/SSE connections. Built
// once at init; instrument-creation failure (shouldn't happen with our
// hardcoded name, but guard anyway) degrades to a no-op counter instead
// of panicking on Add().
var activeConnectionsCounter = mustActiveConnectionsCounter()

func mustActiveConnectionsCounter() metric.Int64UpDownCounter {
	c, err := otel.Meter("protobridge").Int64UpDownCounter("protobridge.active_connections")
	if err != nil {
		logError(err)
		// Drop back to a no-op meter whose counter never panics; recording
		// just becomes a no-op on Add(). Safer than a nil instrument.
		c, _ = metricnoop.NewMeterProvider().Meter("protobridge").Int64UpDownCounter("protobridge.active_connections")
	}
	return c
}

// RecordConnectionOpen increments the active connections gauge. connType
// ("ws" or "sse") is attached so dashboards can break down by transport.
func RecordConnectionOpen(connType string) {
	activeConnectionsCounter.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("conn_type", connType)))
}

// RecordConnectionClose decrements the active connections gauge.
func RecordConnectionClose(connType string) {
	activeConnectionsCounter.Add(context.Background(), -1,
		metric.WithAttributes(attribute.String("conn_type", connType)))
}

// GracefulShutdownOTel flushes and shuts down OTel providers.
func GracefulShutdownOTel(shutdown OTelShutdown) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		logError(err)
	}
}
