package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// --- DurableOption coverage --------------------------------------------

func TestResolveDurableConfig_Defaults(t *testing.T) {
	c := events.ResolveDurableConfig("orders.created")
	if c.AckWait != 30*time.Second {
		t.Errorf("AckWait default = %v", c.AckWait)
	}
	if c.MaxDeliver != 5 {
		t.Errorf("MaxDeliver default = %d", c.MaxDeliver)
	}
	if c.MaxInFlight != 1 {
		t.Errorf("MaxInFlight default = %d", c.MaxInFlight)
	}
	if c.DeadLetterSubject != "orders.created.dlq" {
		t.Errorf("DeadLetterSubject default = %q", c.DeadLetterSubject)
	}
}

func TestResolveDurableConfig_Overrides(t *testing.T) {
	c := events.ResolveDurableConfig("x",
		events.WithAckWait(120*time.Second),
		events.WithMaxDeliver(10),
		events.WithMaxInFlight(8),
		events.WithDeadLetterSubject("custom.dlq"),
	)
	if c.AckWait != 120*time.Second {
		t.Errorf("AckWait = %v", c.AckWait)
	}
	if c.MaxDeliver != 10 {
		t.Errorf("MaxDeliver = %d", c.MaxDeliver)
	}
	if c.MaxInFlight != 8 {
		t.Errorf("MaxInFlight = %d", c.MaxInFlight)
	}
	if c.DeadLetterSubject != "custom.dlq" {
		t.Errorf("DeadLetterSubject = %q", c.DeadLetterSubject)
	}
}

func TestResolveDurableConfig_NonPositiveOptionsAreIgnored(t *testing.T) {
	// Negative / zero values must not clobber the defaults — that's the
	// contract that lets generated codegen emit options unconditionally.
	c := events.ResolveDurableConfig("x",
		events.WithAckWait(0),
		events.WithMaxDeliver(-1),
		events.WithMaxInFlight(0),
	)
	if c.AckWait != 30*time.Second {
		t.Errorf("AckWait should keep default, got %v", c.AckWait)
	}
	if c.MaxDeliver != 5 {
		t.Errorf("MaxDeliver should keep default, got %d", c.MaxDeliver)
	}
	if c.MaxInFlight != 1 {
		t.Errorf("MaxInFlight should keep default, got %d", c.MaxInFlight)
	}
}

// --- NewJetStreamBus error paths ---------------------------------------

func TestNewJetStreamBus_RequiresStreamSubjects(t *testing.T) {
	_, err := events.NewJetStreamBus(context.Background(), events.JetStreamConfig{
		NATSURL: "nats://127.0.0.1:4222",
	})
	if err == nil || !strings.Contains(err.Error(), "StreamSubjects") {
		t.Fatalf("expected StreamSubjects error, got %v", err)
	}
}

func TestNewJetStreamBus_RequiresConnOrURL(t *testing.T) {
	_, err := events.NewJetStreamBus(context.Background(), events.JetStreamConfig{
		StreamSubjects: []string{"x.>"},
	})
	if err == nil || !strings.Contains(err.Error(), "Conn or NATSURL") {
		t.Fatalf("expected Conn-or-URL error, got %v", err)
	}
}

func TestNewJetStreamBus_BadURL(t *testing.T) {
	// Unreachable URL → connect error surfaces.
	_, err := events.NewJetStreamBus(context.Background(), events.JetStreamConfig{
		NATSURL:        "nats://127.0.0.1:1", // no NATS on port 1
		StreamSubjects: []string{"x.>"},
	})
	if err == nil {
		t.Fatal("expected connect error on unreachable URL")
	}
}

// --- JetStreamBus.SubscribeDurable error paths -------------------------

func TestJetStreamBus_SubscribeDurableEmptyGroupFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	_, err := bus.SubscribeDurable("tasks.x", "", func(_ context.Context, _ events.Message) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "non-empty group") {
		t.Fatalf("expected empty-group error, got %v", err)
	}
}

func TestJetStreamBus_OperationsAfterCloseFail(t *testing.T) {
	bus := newTestJetStreamBus(t)
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("second Close should be idempotent, got %v", err)
	}
	if _, err := bus.SubscribeDurable("tasks.x", "g", nil); err == nil {
		t.Error("expected error from SubscribeDurable after close")
	}
	if _, err := bus.SubscribeBroadcast("tasks.x", nil); err == nil {
		t.Error("expected error from SubscribeBroadcast after close")
	}
}

func TestJetStreamBus_PublishUnknownKindFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	err := bus.Publish(context.Background(), "tasks.x", []byte("y"), events.Kind(99), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("expected unknown-kind error, got %v", err)
	}
}

func TestJetStreamBus_PublishBroadcastDoesNotError(t *testing.T) {
	// Core-NATS publish is fire-and-forget; even with no subscribers it
	// returns nil. Exercises the broadcast happy path independent of the
	// other tests' fixture wiring.
	bus := newTestJetStreamBus(t)
	if err := bus.Publish(context.Background(), "notify.lonely", []byte("x"), events.KindBroadcast, nil); err != nil {
		t.Errorf("broadcast publish: %v", err)
	}
}

func TestJetStreamBus_PublishDurableOutOfStreamFails(t *testing.T) {
	// The fixture stream binds tasks.> + notify.> — a publish to
	// outside.x has no stream to land on, so JetStream returns an error
	// (no_stream_response). Surfaces the durable error path.
	bus := newTestJetStreamBus(t)
	err := bus.Publish(context.Background(), "outside.x", []byte("x"), events.KindDurable, nil)
	if err == nil {
		t.Fatal("expected durable publish error for unbound subject")
	}
}

// --- DLQ disabled via "-" sentinel -------------------------------------

func TestJetStreamBus_DLQSentinelSkipsForwarding(t *testing.T) {
	bus := newTestJetStreamBus(t)

	// Subscribe to where the DLQ would normally land — should never fire.
	dlqHit := make(chan struct{}, 1)
	_, err := bus.SubscribeBroadcast("tasks.silent.dlq", func(_ context.Context, _ events.Message) error {
		select {
		case dlqHit <- struct{}{}:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = bus.SubscribeDurable("tasks.silent", "workers", func(_ context.Context, m events.Message) error {
		m.Nack()
		return errors.New("permafail")
	},
		events.WithAckWait(80*time.Millisecond),
		events.WithMaxDeliver(2),
		events.WithDeadLetterSubject("-"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(context.Background(), "tasks.silent", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatal(err)
	}

	// Wait long enough for both deliveries + the DLQ-skip decision to settle.
	select {
	case <-dlqHit:
		t.Fatal("DLQ subject was hit despite '-' sentinel")
	case <-time.After(500 * time.Millisecond):
	}
}

// --- Heartbeat option threading via codegen-style call -----------------

func TestJetStreamBus_CallerOptsOverrideAnnotation(t *testing.T) {
	// The generated wrapper appends caller opts after annotation defaults
	// (covered by ResolveDurableConfig's append semantics). Spot-check
	// that the runtime applies the last writer.
	c := events.ResolveDurableConfig("x",
		events.WithAckWait(10*time.Second),
		events.WithAckWait(60*time.Second),
	)
	if c.AckWait != 60*time.Second {
		t.Errorf("last-writer-wins broken: AckWait = %v", c.AckWait)
	}
}
