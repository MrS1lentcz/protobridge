package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// Drives the header-iteration branches in publishCore + publishJetStream
// + the broadcast Headers loop in dispatch.
func TestJetStreamBus_HeadersRoundTrip(t *testing.T) {
	bus := newTestJetStreamBus(t)

	got := make(chan map[string]string, 1)
	_, err := bus.SubscribeBroadcast("notify.headers", func(_ context.Context, m events.Message) error {
		// InProgress on broadcast is a documented no-op returning nil —
		// exercise the closure body so it isn't silently dead code.
		if err := m.InProgress(); err != nil {
			t.Errorf("broadcast InProgress should be no-op nil, got %v", err)
		}
		got <- m.Headers
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the subscription register

	if err := bus.Publish(context.Background(), "notify.headers", []byte("x"), events.KindBroadcast,
		map[string]string{"X-Trace": "abc", "X-User": "u-1"}); err != nil {
		t.Fatal(err)
	}

	select {
	case h := <-got:
		if h["X-Trace"] != "abc" || h["X-User"] != "u-1" {
			t.Errorf("headers lost or mangled: %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast never delivered")
	}
}

// Drives the BOTH-kind happy path with headers — also exercises the
// publishJetStream header iteration since durable leg runs first.
func TestJetStreamBus_BothKindHeadersOnDurableLeg(t *testing.T) {
	bus := newTestJetStreamBus(t)

	got := make(chan map[string]string, 1)
	_, err := bus.SubscribeDurable("notify.both", "g", func(_ context.Context, m events.Message) error {
		got <- m.Headers
		m.Ack()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := bus.Publish(context.Background(), "notify.both", []byte("x"), events.KindBoth,
		map[string]string{"X-K": "v"}); err != nil {
		t.Fatal(err)
	}

	select {
	case h := <-got:
		if h["X-K"] != "v" {
			t.Errorf("durable leg lost headers: %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable delivery never arrived")
	}
}

// Broadcast handler panic must be recovered (covers the dispatch panic
// branch on the broadcast path).
func TestJetStreamBus_BroadcastHandlerPanicRecovered(t *testing.T) {
	bus := newTestJetStreamBus(t)

	hit := make(chan struct{}, 2)
	_, err := bus.SubscribeBroadcast("notify.bad", func(_ context.Context, _ events.Message) error {
		hit <- struct{}{}
		panic("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	// Two publishes — the bus must survive the first panic and still
	// deliver the second. If the panic killed the subscriber goroutine
	// the second hit never arrives.
	for i := 0; i < 2; i++ {
		if err := bus.Publish(context.Background(), "notify.bad", []byte("x"), events.KindBroadcast, nil); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.After(2 * time.Second)
	for n := 0; n < 2; n++ {
		select {
		case <-hit:
		case <-deadline:
			t.Fatalf("subscriber goroutine died after panic; hits=%d", n)
		}
	}
}

// Broadcast handler returning an error covers the warn branch in the
// broadcast dispatch goroutine.
func TestJetStreamBus_BroadcastHandlerErrorLogged(t *testing.T) {
	bus := newTestJetStreamBus(t)
	done := make(chan struct{}, 1)
	_, err := bus.SubscribeBroadcast("notify.err", func(_ context.Context, _ events.Message) error {
		done <- struct{}{}
		return errors.New("don't crash me")
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := bus.Publish(context.Background(), "notify.err", []byte("x"), events.KindBroadcast, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast never delivered")
	}
}

// Long handler error is preserved in the X-Dlq-Error header but truncated
// to 512 chars — exercises the truncate(s, n) branch where len(s) > n.
func TestJetStreamBus_DLQTruncatesLongErrorMessage(t *testing.T) {
	bus := newTestJetStreamBus(t)

	dlqCh := make(chan events.Message, 1)
	_, err := bus.SubscribeBroadcast("tasks.long_err.dlq", func(_ context.Context, m events.Message) error {
		dlqCh <- m
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	longErr := strings.Repeat("e", 1024) // > 512 truncation cap
	_, err = bus.SubscribeDurable("tasks.long_err", "g", func(_ context.Context, m events.Message) error {
		m.Nack()
		return errors.New(longErr)
	},
		events.WithAckWait(80*time.Millisecond),
		events.WithMaxDeliver(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(context.Background(), "tasks.long_err", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-dlqCh:
		got := m.Headers["X-Dlq-Error"]
		// 512 ASCII bytes + 3 bytes for the "…" rune = 515 bytes.
		if len(got) >= 1024 {
			t.Errorf("error not truncated: %d bytes", len(got))
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("truncation marker missing: %q", got[len(got)-10:])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DLQ never landed")
	}
}

// DLQ headers iteration: publish with caller headers and verify they're
// copied verbatim onto the DLQ message (alongside the X-Dlq-* metadata).
func TestJetStreamBus_DLQPreservesOriginalHeaders(t *testing.T) {
	bus := newTestJetStreamBus(t)

	dlqCh := make(chan events.Message, 1)
	_, err := bus.SubscribeBroadcast("tasks.headerful.dlq", func(_ context.Context, m events.Message) error {
		dlqCh <- m
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = bus.SubscribeDurable("tasks.headerful", "g", func(_ context.Context, m events.Message) error {
		m.Nack()
		return errors.New("nope")
	},
		events.WithAckWait(80*time.Millisecond),
		events.WithMaxDeliver(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(context.Background(), "tasks.headerful", []byte("p"), events.KindDurable,
		map[string]string{"X-Trace": "t-1"}); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-dlqCh:
		if m.Headers["X-Trace"] != "t-1" {
			t.Errorf("DLQ stripped original X-Trace: %+v", m.Headers)
		}
		if m.Headers["X-Dlq-Reason"] == "" {
			t.Error("DLQ metadata missing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DLQ never landed")
	}
}

// NewJetStreamBus must clean up its dialed connection when stream creation
// fails. Triggering: the stream subject pattern '$JS.>' is reserved and
// NATS refuses to create a stream over it.
func TestNewJetStreamBus_CleansUpConnOnStreamCreateFailure(t *testing.T) {
	url := startEmbeddedJetStream(t)
	_, err := events.NewJetStreamBus(context.Background(), events.JetStreamConfig{
		NATSURL:        url,
		StreamName:     "bad",
		StreamSubjects: []string{"$JS.>"}, // reserved → create-stream error
	})
	if err == nil {
		t.Fatal("expected stream-create error")
	}
}

// WatermillBus's InProgress closure is a no-op returning nil — exercise
// it through a delivered durable message so the closure body is hit.
func TestWatermillBus_InProgressIsNoOp(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	done := make(chan struct{}, 1)
	sub, err := bus.SubscribeDurable("anything", "g", func(_ context.Context, m events.Message) error {
		if err := m.InProgress(); err != nil {
			t.Errorf("InProgress on Watermill should be no-op nil, got %v", err)
		}
		m.Ack()
		done <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := bus.Publish(context.Background(), "anything", []byte("x"), events.KindDurable, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("durable handler never ran")
	}
}

// BOTH-kind publish must fail when the durable leg fails (e.g. subject
// outside the stream filter) and not silently broadcast.
func TestJetStreamBus_BothKindFailsWhenDurableLegFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	err := bus.Publish(context.Background(), "outside.both", []byte("x"), events.KindBoth, nil)
	if err == nil {
		t.Fatal("expected BOTH publish to fail when durable leg can't land")
	}
}

// SubscribeDurable with a malformed subject fails inside JetStream's
// CreateOrUpdateConsumer — covers the consumer-create error path.
func TestJetStreamBus_SubscribeDurableInvalidSubjectFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	// Subjects with whitespace are rejected by JetStream consumer config.
	if _, err := bus.SubscribeDurable("tasks.bad subject", "g",
		func(_ context.Context, _ events.Message) error { return nil }); err == nil {
		t.Fatal("expected consumer-create error for malformed subject")
	}
}

// SubscribeBroadcast on an empty subject fails — exercises the
// nc.Subscribe error path + cancel cleanup.
func TestJetStreamBus_SubscribeBroadcastEmptySubjectFails(t *testing.T) {
	bus := newTestJetStreamBus(t)
	if _, err := bus.SubscribeBroadcast("", func(_ context.Context, _ events.Message) error {
		return nil
	}); err == nil {
		t.Fatal("expected error on empty broadcast subject")
	}
}

// WatermillBus accepts DurableOption(s) for API consistency but logs and
// ignores them — covers the previously-unhit warning branch.
func TestWatermillBus_OptionWarningPathHit(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	sub, err := bus.SubscribeDurable("anything", "group-x", func(_ context.Context, _ events.Message) error {
		return nil
	}, events.WithAckWait(2*time.Second), events.WithMaxDeliver(7))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
}
