package events

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics emitted by JetStreamBus, exposed via the usual OTel → Prometheus
// pipeline configured in runtime/otel.go. Names are intentionally kept
// stable for dashboard compatibility; attribute keys (`subject`, `group`,
// `result`, `reason`) mirror the original spec.
//
// Instrument creation errors fall through to the OTel SDK's built-in
// no-op instruments — the SDK never returns a nil instrument, so
// recording is always safe even when the meter provider is misconfigured.
var (
	durableInflight  metric.Int64UpDownCounter
	durableProcessed metric.Int64Counter
	durableDuration  metric.Float64Histogram
	dlqTotal         metric.Int64Counter
)

func init() {
	m := otel.Meter("protobridge/events")
	durableInflight, _ = m.Int64UpDownCounter(
		"protobridge_durable_messages_inflight",
		metric.WithDescription("Durable messages currently being processed per subject/group."),
	)
	durableProcessed, _ = m.Int64Counter(
		"protobridge_durable_messages_processed_total",
		metric.WithDescription("Durable messages completed, tagged by result (ack|nack|panic|dlq)."),
	)
	durableDuration, _ = m.Float64Histogram(
		"protobridge_durable_handler_duration_seconds",
		metric.WithDescription("Handler wall-clock duration per durable delivery."),
	)
	dlqTotal, _ = m.Int64Counter(
		"protobridge_durable_dlq_total",
		metric.WithDescription("Messages forwarded to the dead-letter subject per original subject/group."),
	)
}

// recordDurableStart increments the in-flight gauge and returns a
// finalizer that, when called with a result label, records duration +
// decrements in-flight + bumps the processed counter. Callers defer the
// finalizer so every code path (including panic recovery) closes out.
func recordDurableStart(subject, group string) func(result string) {
	attrs := metric.WithAttributes(
		attribute.String("subject", subject),
		attribute.String("group", group),
	)
	durableInflight.Add(context.Background(), 1, attrs)
	start := time.Now()
	return func(result string) {
		durableInflight.Add(context.Background(), -1, attrs)
		durableDuration.Record(context.Background(), time.Since(start).Seconds(), attrs)
		durableProcessed.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("subject", subject),
				attribute.String("group", group),
				attribute.String("result", result),
			))
	}
}

func recordDLQ(subject, group, reason string) {
	dlqTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("subject", subject),
			attribute.String("group", group),
			attribute.String("reason", reason),
		))
}
