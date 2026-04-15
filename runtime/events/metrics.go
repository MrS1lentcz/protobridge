package events

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// Metrics emitted by JetStreamBus, exposed via the usual OTel → Prometheus
// pipeline configured in runtime/otel.go. Names are intentionally kept
// stable for dashboard compatibility; attribute keys (`subject`, `group`,
// `result`, `reason`) mirror the spec.
//
// Instruments are created once at package load. Creation failure degrades
// to a no-op meter so a misconfigured OTel SDK never panics a running
// Bus — the metric just stops recording.
var (
	durableInflight  metric.Int64UpDownCounter
	durableProcessed metric.Int64Counter
	durableDuration  metric.Float64Histogram
	dlqTotal         metric.Int64Counter
)

func init() {
	m := otel.Meter("protobridge/events")
	var err error
	if durableInflight, err = m.Int64UpDownCounter(
		"protobridge_durable_messages_inflight",
		metric.WithDescription("Durable messages currently being processed per subject/group."),
	); err != nil {
		durableInflight = noopMeter().Int64UpDownCounterNoop()
	}
	if durableProcessed, err = m.Int64Counter(
		"protobridge_durable_messages_processed_total",
		metric.WithDescription("Durable messages completed, tagged by result (ack|nack|panic|dlq)."),
	); err != nil {
		durableProcessed = noopMeter().Int64CounterNoop()
	}
	if durableDuration, err = m.Float64Histogram(
		"protobridge_durable_handler_duration_seconds",
		metric.WithDescription("Handler wall-clock duration per durable delivery."),
	); err != nil {
		durableDuration = noopMeter().Float64HistogramNoop()
	}
	if dlqTotal, err = m.Int64Counter(
		"protobridge_durable_dlq_total",
		metric.WithDescription("Messages forwarded to the dead-letter subject per original subject/group."),
	); err != nil {
		dlqTotal = noopMeter().Int64CounterNoop()
	}
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

// noopMeter returns a noop-specific Meter; the OTel noop package returns
// no-op instruments whose Add/Record are cheap no-ops. Used as a graceful
// fallback when instrument creation fails.
type noopInstrument struct{ metric.Meter }

func noopMeter() noopInstrument {
	return noopInstrument{metricnoop.NewMeterProvider().Meter("protobridge/events/noop")}
}

func (n noopInstrument) Int64UpDownCounterNoop() metric.Int64UpDownCounter {
	c, _ := n.Meter.Int64UpDownCounter("noop")
	return c
}

func (n noopInstrument) Int64CounterNoop() metric.Int64Counter {
	c, _ := n.Meter.Int64Counter("noop")
	return c
}

func (n noopInstrument) Float64HistogramNoop() metric.Float64Histogram {
	h, _ := n.Meter.Float64Histogram("noop")
	return h
}
