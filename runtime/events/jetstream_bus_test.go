package events_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// startEmbeddedJetStream boots an in-process NATS server with JetStream
// enabled and a temp storage dir. It returns the client URL and registers
// t.Cleanup to stop the server.
func startEmbeddedJetStream(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	opts := &natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  dir,
		NoLog:     true,
		NoSigs:    true,
	}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("start nats: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		srv.Shutdown()
		t.Fatalf("nats not ready")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv.ClientURL()
}

func newTestJetStreamBus(t *testing.T) *events.JetStreamBus {
	t.Helper()
	url := startEmbeddedJetStream(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Use a stream name unique per test so parallel tests don't share state
	// when they reuse a single server (not the current setup, but harmless).
	bus, err := events.NewJetStreamBus(ctx, events.JetStreamConfig{
		NATSURL:        url,
		StreamName:     "protobridge_test",
		StreamSubjects: []string{"tasks.>", "notify.>"},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// --- Acceptance test 1: long-running handler with heartbeat ------------

func TestJetStreamBus_LongHandlerHeartbeatPreventsRedelivery(t *testing.T) {
	bus := newTestJetStreamBus(t)

	var deliveries atomic.Int32
	done := make(chan struct{})

	sub, err := bus.SubscribeDurable("tasks.long", "workers", func(ctx context.Context, m events.Message) error {
		n := deliveries.Add(1)
		if n > 1 {
			// Unexpected redelivery — fail the test.
			m.Nack()
			return errors.New("unexpected redelivery")
		}
		// Simulate a slow handler. AckWait=500ms means two heartbeats suffice.
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(1500 * time.Millisecond)
		for {
			select {
			case <-ticker.C:
				if err := m.InProgress(); err != nil {
					t.Errorf("InProgress: %v", err)
				}
			case <-deadline:
				m.Ack()
				close(done)
				return nil
			}
		}
	}, events.WithAckWait(500*time.Millisecond))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Publish(context.Background(), "tasks.long", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("handler never completed; deliveries=%d", deliveries.Load())
	}

	// Give any stray redelivery ~1 AckWait to show up.
	time.Sleep(700 * time.Millisecond)
	if got := deliveries.Load(); got != 1 {
		t.Errorf("expected 1 delivery, got %d", got)
	}
}

// --- Acceptance test 2: handler that stops heartbeating gets redelivered -

func TestJetStreamBus_StaleHandlerTriggersRedelivery(t *testing.T) {
	bus := newTestJetStreamBus(t)

	var deliveries atomic.Int32
	second := make(chan struct{}, 1)

	sub, err := bus.SubscribeDurable("tasks.stale", "workers", func(ctx context.Context, m events.Message) error {
		n := deliveries.Add(1)
		if n == 1 {
			// First delivery: hold the message past AckWait without
			// heartbeating. JetStream should redeliver.
			time.Sleep(800 * time.Millisecond)
			m.Ack() // too late — already redelivered
			return nil
		}
		m.Ack()
		select {
		case second <- struct{}{}:
		default:
		}
		return nil
	}, events.WithAckWait(200*time.Millisecond), events.WithMaxDeliver(5))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Publish(context.Background(), "tasks.stale", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Fatalf("redelivery never happened; deliveries=%d", deliveries.Load())
	}
	if got := deliveries.Load(); got < 2 {
		t.Errorf("expected >=2 deliveries, got %d", got)
	}
}

// --- Acceptance test 3: MaxDeliver exhausted → DLQ ----------------------

func TestJetStreamBus_ExhaustedDeliveryRoutesToDLQ(t *testing.T) {
	bus := newTestJetStreamBus(t)

	// DLQ subscriber: plain broadcast is enough since DLQ publish uses
	// JetStream but we can subscribe via core NATS on the same subject.
	dlqCh := make(chan events.Message, 2)
	dlqSub, err := bus.SubscribeBroadcast("tasks.fail.dlq", func(ctx context.Context, m events.Message) error {
		dlqCh <- m
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe dlq: %v", err)
	}
	defer func() { _ = dlqSub.Unsubscribe() }()

	// Always-nack handler: should redeliver MaxDeliver=3 times, then DLQ.
	var deliveries atomic.Int32
	sub, err := bus.SubscribeDurable("tasks.fail", "workers", func(ctx context.Context, m events.Message) error {
		deliveries.Add(1)
		m.Nack()
		return errors.New("permanent fail")
	},
		events.WithAckWait(150*time.Millisecond),
		events.WithMaxDeliver(3),
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Publish(context.Background(), "tasks.fail", []byte("poison"), events.KindDurable, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case m := <-dlqCh:
		if m.Headers["X-Dlq-Reason"] != "max_deliver_exceeded" {
			t.Errorf("DLQ reason = %q, want max_deliver_exceeded", m.Headers["X-Dlq-Reason"])
		}
		if m.Headers["X-Dlq-Original-Subject"] != "tasks.fail" {
			t.Errorf("DLQ original subject = %q", m.Headers["X-Dlq-Original-Subject"])
		}
		if string(m.Payload) != "poison" {
			t.Errorf("DLQ payload = %q, want poison", m.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("DLQ message never arrived; deliveries=%d", deliveries.Load())
	}

	if got := deliveries.Load(); got != 3 {
		t.Errorf("expected exactly 3 deliveries before DLQ, got %d", got)
	}
}

// --- Acceptance test 4: two subscribers in one group load-balance -------

func TestJetStreamBus_GroupLoadBalance(t *testing.T) {
	bus := newTestJetStreamBus(t)

	var a, b atomic.Int32
	handler := func(counter *atomic.Int32) events.Handler {
		return func(ctx context.Context, m events.Message) error {
			counter.Add(1)
			m.Ack()
			return nil
		}
	}
	sub1, err := bus.SubscribeDurable("tasks.balanced", "workers", handler(&a))
	if err != nil {
		t.Fatalf("sub1: %v", err)
	}
	defer func() { _ = sub1.Unsubscribe() }()
	sub2, err := bus.SubscribeDurable("tasks.balanced", "workers", handler(&b))
	if err != nil {
		t.Fatalf("sub2: %v", err)
	}
	defer func() { _ = sub2.Unsubscribe() }()

	const n = 20
	for i := 0; i < n; i++ {
		if err := bus.Publish(context.Background(), "tasks.balanced", []byte(fmt.Sprintf("%d", i)), events.KindDurable, nil); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.After(3 * time.Second)
	for a.Load()+b.Load() < n {
		select {
		case <-deadline:
			t.Fatalf("load-balance stalled: a=%d b=%d", a.Load(), b.Load())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	if a.Load() == 0 || b.Load() == 0 {
		t.Errorf("expected both subscribers to receive at least 1 message, a=%d b=%d",
			a.Load(), b.Load())
	}
	if total := a.Load() + b.Load(); total != n {
		t.Errorf("total deliveries %d, want exactly %d (no dup)", total, n)
	}
}

// --- Acceptance test 5: panic in handler triggers redelivery -----------

func TestJetStreamBus_PanicTriggersRedelivery(t *testing.T) {
	bus := newTestJetStreamBus(t)

	var deliveries atomic.Int32
	done := make(chan struct{}, 1)

	sub, err := bus.SubscribeDurable("tasks.panic", "workers", func(ctx context.Context, m events.Message) error {
		n := deliveries.Add(1)
		if n == 1 {
			panic("boom")
		}
		m.Ack()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	},
		events.WithAckWait(200*time.Millisecond),
		events.WithMaxDeliver(5),
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Publish(context.Background(), "tasks.panic", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("redelivery after panic never happened; deliveries=%d", deliveries.Load())
	}
	if got := deliveries.Load(); got < 2 {
		t.Errorf("expected >=2 deliveries after panic, got %d", got)
	}
}

// --- BOTH-kind smoke test: durable + broadcast legs both fire ----------

func TestJetStreamBus_BothKindDeliversToBothPaths(t *testing.T) {
	bus := newTestJetStreamBus(t)

	var broadcastHits atomic.Int32
	var durableHits atomic.Int32

	bsub, err := bus.SubscribeBroadcast("notify.test", func(ctx context.Context, m events.Message) error {
		broadcastHits.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bsub.Unsubscribe() }()

	dsub, err := bus.SubscribeDurable("notify.test", "workers", func(ctx context.Context, m events.Message) error {
		durableHits.Add(1)
		m.Ack()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dsub.Unsubscribe() }()

	// Let consumer registration settle.
	time.Sleep(100 * time.Millisecond)

	if err := bus.Publish(context.Background(), "notify.test", []byte("hi"), events.KindBoth, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for broadcastHits.Load() == 0 || durableHits.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("BOTH delivery incomplete: broadcast=%d durable=%d",
				broadcastHits.Load(), durableHits.Load())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// --- Publish after Close surfaces an error ------------------------------

func TestJetStreamBus_PublishAfterCloseFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	if err := bus.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	err := bus.Publish(context.Background(), "x", []byte("y"), events.KindDurable, nil)
	if err == nil {
		t.Fatal("expected error on publish after close")
	}
}

// --- Nats connection reuse (caller-owned Conn not closed by Close) -----

func TestJetStreamBus_ReusesCallerOwnedConn(t *testing.T) {
	url := startEmbeddedJetStream(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	bus, err := events.NewJetStreamBus(context.Background(), events.JetStreamConfig{
		Conn:           nc,
		StreamName:     "caller_owned",
		StreamSubjects: []string{"anything.>"},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	_ = bus.Close()

	if !nc.IsConnected() {
		t.Error("caller-owned Conn should still be connected after bus.Close")
	}
}

func TestJetStreamBus_BroadcastAckNackAreNoops(t *testing.T) {
	// Broadcast subscriptions have no ack semantics — Ack/Nack on the
	// delivered Message are wired to no-op closures. Exercising them must
	// not panic and must not affect further delivery.
	bus := newTestJetStreamBus(t)

	received := make(chan struct{}, 1)
	sub, err := bus.SubscribeBroadcast("notify.noop", func(_ context.Context, m events.Message) error {
		m.Ack()
		m.Nack()
		if err := m.InProgress(); err != nil {
			t.Errorf("broadcast InProgress must be a no-op, got %v", err)
		}
		received <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	time.Sleep(50 * time.Millisecond) // let subscription settle
	if err := bus.Publish(context.Background(), "notify.noop", []byte("x"), events.KindBroadcast, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast delivery never arrived")
	}
}

func TestJetStreamBus_BroadcastHandlerErrorIsLogged(t *testing.T) {
	// A handler returning non-nil is logged but the subscription stays
	// healthy. Exercises the Warn branch.
	bus := newTestJetStreamBus(t)

	received := make(chan struct{}, 1)
	sub, err := bus.SubscribeBroadcast("notify.warn", func(_ context.Context, _ events.Message) error {
		received <- struct{}{}
		return errors.New("soft failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	time.Sleep(50 * time.Millisecond)
	if err := bus.Publish(context.Background(), "notify.warn", []byte("x"), events.KindBroadcast, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast delivery never arrived")
	}
}

func init() {
	// keep the default logger quiet during tests
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}
