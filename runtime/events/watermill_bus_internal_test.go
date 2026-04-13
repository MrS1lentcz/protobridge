package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	wmsg "github.com/ThreeDotsLabs/watermill/message"
)

// failingPublisher returns the configured error from every Publish call —
// lets us cover the broadcast-best-effort + durable-error branches without
// rigging a real broken backend.
type failingPublisher struct {
	err error
}

func (p *failingPublisher) Publish(_ string, _ ...*wmsg.Message) error { return p.err }
func (p *failingPublisher) Close() error                                { return nil }

// failingSubscriber refuses to subscribe — covers the Subscribe error path.
type failingSubscriber struct {
	err error
}

func (s *failingSubscriber) Subscribe(_ context.Context, _ string) (<-chan *wmsg.Message, error) {
	return nil, s.err
}
func (s *failingSubscriber) Close() error { return nil }

func TestPublish_BroadcastBestEffortLogsAndReturnsNil(t *testing.T) {
	bus := &WatermillBus{
		BroadcastPublisher: &failingPublisher{err: errors.New("bus down")},
		DurablePublisher:   &failingPublisher{err: nil}, // unused on broadcast path
	}
	t.Cleanup(func() { _ = bus.Close() })

	// Broadcast publish error is intentionally swallowed — caller must not
	// see it. The warning lands in slog.Default which we don't capture here;
	// we just assert the contract.
	if err := bus.Publish(context.Background(), "x", []byte("y"), KindBroadcast, nil); err != nil {
		t.Errorf("broadcast publish error must not surface to caller: %v", err)
	}
}

func TestPublish_DurableSurfacesError(t *testing.T) {
	want := errors.New("durable down")
	bus := &WatermillBus{DurablePublisher: &failingPublisher{err: want}}
	t.Cleanup(func() { _ = bus.Close() })

	err := bus.Publish(context.Background(), "x", []byte("y"), KindDurable, nil)
	if !errors.Is(err, want) {
		t.Errorf("expected wrapped %v, got %v", want, err)
	}
}

func TestPublish_BothDurableErrorStops(t *testing.T) {
	want := errors.New("durable down")
	broadcastCalled := false
	bus := &WatermillBus{
		DurablePublisher: &failingPublisher{err: want},
		BroadcastPublisher: &countingPublisher{cb: func() { broadcastCalled = true }},
	}
	t.Cleanup(func() { _ = bus.Close() })

	err := bus.Publish(context.Background(), "x", []byte("y"), KindBoth, nil)
	if !errors.Is(err, want) {
		t.Errorf("expected durable error to bubble: %v", err)
	}
	if broadcastCalled {
		t.Error("broadcast leg of BOTH must not fire when durable fails")
	}
}

func TestPublish_BothBroadcastFailureSwallowed(t *testing.T) {
	bus := &WatermillBus{
		DurablePublisher:   &countingPublisher{},
		BroadcastPublisher: &failingPublisher{err: errors.New("broadcast down")},
	}
	t.Cleanup(func() { _ = bus.Close() })

	if err := bus.Publish(context.Background(), "x", []byte("y"), KindBoth,
		map[string]string{"trace": "abc"}); err != nil {
		t.Errorf("BOTH must not surface broadcast errors: %v", err)
	}
}

func TestPublish_UnknownKindErrors(t *testing.T) {
	bus := &WatermillBus{DurablePublisher: &countingPublisher{}, BroadcastPublisher: &countingPublisher{}}
	t.Cleanup(func() { _ = bus.Close() })

	err := bus.Publish(context.Background(), "x", []byte("y"), Kind(99), nil)
	if err == nil || !contains(err.Error(), "unknown kind") {
		t.Errorf("expected unknown-kind error, got %v", err)
	}
}

func TestSubscribe_NilSubscriberRejected(t *testing.T) {
	bus := &WatermillBus{} // no subscribers configured
	if _, err := bus.SubscribeBroadcast("x", noopHandler); err == nil {
		t.Error("nil subscriber must be rejected")
	}
	if _, err := bus.SubscribeDurable("x", "g", noopHandler); err == nil {
		t.Error("nil subscriber must be rejected")
	}
}

func TestSubscribe_ClosedBusRejects(t *testing.T) {
	bus := NewInMemoryBus()
	_ = bus.Close()
	if _, err := bus.SubscribeBroadcast("x", noopHandler); err == nil {
		t.Error("subscribe on closed bus must error")
	}
	if _, err := bus.SubscribeDurable("x", "g", noopHandler); err == nil {
		t.Error("subscribe on closed bus must error")
	}
}

func TestSubscribe_FailingSubscriberCancelled(t *testing.T) {
	want := errors.New("subscribe down")
	bus := &WatermillBus{
		BroadcastSubscriber: &failingSubscriber{err: want},
		DurableSubscriber:   &failingSubscriber{err: want},
	}
	t.Cleanup(func() { _ = bus.Close() })

	_, err := bus.SubscribeBroadcast("x", noopHandler)
	if !errors.Is(err, want) {
		t.Errorf("expected wrapped subscribe error, got %v", err)
	}
}

func TestDispatch_HandlerErrorTriggersNack(t *testing.T) {
	bus := NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	done := make(chan struct{})
	sub, err := bus.SubscribeBroadcast("x", func(_ context.Context, _ Message) error {
		close(done)
		return errors.New("handler boom")
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "x", []byte("p"), KindBroadcast, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
	// No assertion on Nack-side effect — gochannel ignores it; the goal is
	// to exercise the dispatch error → m.Nack() path without panicking.
}

func TestDispatch_PanicRecovered(t *testing.T) {
	bus := NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	done := make(chan struct{})
	sub, err := bus.SubscribeBroadcast("x", func(_ context.Context, _ Message) error {
		defer close(done)
		panic("oops")
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "x", []byte("p"), KindBroadcast, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
	// Subscriber goroutine must still be alive after panic — publish another
	// message and verify it lands. (We rebind handler via a fresh subscription
	// because the panic in the prior handler doesn't hurt our process.)
	got := make(chan struct{})
	sub2, _ := bus.SubscribeBroadcast("x2", func(_ context.Context, _ Message) error {
		close(got)
		return nil
	})
	defer sub2.Unsubscribe() //nolint:errcheck
	_ = bus.Publish(context.Background(), "x2", []byte("p"), KindBroadcast, nil)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop died after panic")
	}
}

func TestClose_IdempotentAndStopsPublish(t *testing.T) {
	bus := NewInMemoryBus()
	if err := bus.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("second close should be no-op, got %v", err)
	}
	if err := bus.Publish(context.Background(), "x", []byte("p"), KindBroadcast, nil); err == nil {
		t.Error("publish after close should error")
	}
}

func TestLogger_DefaultsToSlogDefault(t *testing.T) {
	// Without setting Logger, calls to logger() must return a non-nil
	// *slog.Logger so subsequent .Warn/.Error calls don't panic.
	bus := &WatermillBus{}
	if bus.logger() == nil {
		t.Fatal("logger() must never return nil")
	}
}

func TestPublish_NilPublisherReturnsConfigError(t *testing.T) {
	cases := []struct {
		name string
		bus  *WatermillBus
		kind Kind
		want string
	}{
		{"broadcast nil", &WatermillBus{}, KindBroadcast, "broadcast publisher not configured"},
		{"durable nil", &WatermillBus{}, KindDurable, "durable publisher not configured"},
		{"both missing one", &WatermillBus{DurablePublisher: &countingPublisher{}}, KindBoth, "both durable and broadcast"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.bus.Publish(context.Background(), "x", []byte("y"), tc.kind, nil)
			if err == nil || !contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSubscribeDurable_NonEmptyGroupLogsWarning(t *testing.T) {
	// The bus accepts a group parameter for forward-compatibility but
	// today it only logs an info-level warning (Watermill addresses by
	// topic alone). Verify the call still succeeds and registers the
	// subscription.
	bus := NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	sub, err := bus.SubscribeDurable("x", "shipping-group", noopHandler)
	if err != nil {
		t.Fatalf("SubscribeDurable should succeed even with a non-empty group: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("Unsubscribe: %v", err)
	}
}

func TestDispatch_AckNackClosuresInvokeWatermillMessage(t *testing.T) {
	// The Ack/Nack closures stamped on Message wrap the underlying
	// watermill message. Generated subscriber helpers always call one of
	// them before returning; cover the closures by exercising the explicit
	// Ack path through a real handler call.
	bus := NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	done := make(chan struct{})
	sub, err := bus.SubscribeBroadcast("x", func(_ context.Context, m Message) error {
		m.Ack()  // exercises the Ack closure
		m.Nack() // exercises the Nack closure (no-op for gochannel after ack)
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "x", []byte("p"), KindBroadcast, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never ran")
	}
}

func TestLogger_HonoursExplicitLogger(t *testing.T) {
	// When Logger is set, logger() must return that one — covers the
	// non-default branch that the default-to-slog test cannot reach.
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := &WatermillBus{Logger: custom}
	if got := bus.logger(); got != custom {
		t.Errorf("logger() did not return the configured Logger")
	}
}

// --- helpers ---

func noopHandler(_ context.Context, _ Message) error { return nil }

type countingPublisher struct {
	mu  sync.Mutex
	n   int
	cb  func()
}

func (p *countingPublisher) Publish(_ string, _ ...*wmsg.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	if p.cb != nil {
		p.cb()
	}
	return nil
}

func (p *countingPublisher) Close() error { return nil }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
