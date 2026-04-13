package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// TestBus_BroadcastFanOut verifies that two subscribers on the same subject
// both receive every published message. This is the contract every broadcast
// backend must honour and the only sanity check we can run end-to-end
// without a real transport.
func TestBus_BroadcastFanOut(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	var (
		wg       sync.WaitGroup
		got1     [][]byte
		got2     [][]byte
		mu1, mu2 sync.Mutex
	)
	wg.Add(2)

	sub1, err := bus.SubscribeBroadcast("orders.created", func(_ context.Context, m events.Message) error {
		mu1.Lock()
		got1 = append(got1, m.Payload)
		mu1.Unlock()
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe sub1: %v", err)
	}
	defer sub1.Unsubscribe() //nolint:errcheck

	sub2, err := bus.SubscribeBroadcast("orders.created", func(_ context.Context, m events.Message) error {
		mu2.Lock()
		got2 = append(got2, m.Payload)
		mu2.Unlock()
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe sub2: %v", err)
	}
	defer sub2.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "orders.created", []byte("hello"), events.KindBroadcast, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for both subscribers")
	}

	if len(got1) != 1 || string(got1[0]) != "hello" {
		t.Errorf("sub1 got %v", got1)
	}
	if len(got2) != 1 || string(got2[0]) != "hello" {
		t.Errorf("sub2 got %v", got2)
	}
}

// TestBus_DurablePublishHeaders verifies that headers round-trip from
// Publish to the handler — the trace-context propagation contract.
func TestBus_DurablePublishHeaders(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	var (
		got     map[string]string
		mu      sync.Mutex
		ready   = make(chan struct{})
	)
	sub, err := bus.SubscribeDurable("shipments", "worker", func(_ context.Context, m events.Message) error {
		mu.Lock()
		got = m.Headers
		mu.Unlock()
		close(ready)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "shipments", []byte("ship me"), events.KindDurable,
		map[string]string{"traceparent": "00-abc-def-01"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}

	mu.Lock()
	defer mu.Unlock()
	if got["traceparent"] != "00-abc-def-01" {
		t.Errorf("trace header missing: %v", got)
	}
}

// TestBus_BothKindReachesBoth ensures BOTH publishes hit both paths. We
// subscribe one consumer on each side and assert both observe the message.
func TestBus_BothKindReachesBoth(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	var wg sync.WaitGroup
	wg.Add(2)

	subB, err := bus.SubscribeBroadcast("orders.shipped", func(_ context.Context, _ events.Message) error {
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe broadcast: %v", err)
	}
	defer subB.Unsubscribe() //nolint:errcheck

	subD, err := bus.SubscribeDurable("orders.shipped", "shipping-mailer", func(_ context.Context, _ events.Message) error {
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe durable: %v", err)
	}
	defer subD.Unsubscribe() //nolint:errcheck

	if err := bus.Publish(context.Background(), "orders.shipped", []byte("OK"), events.KindBoth, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("both legs did not deliver")
	}
}

func TestBus_PublishAfterCloseFails(t *testing.T) {
	bus := events.NewInMemoryBus()
	if err := bus.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	err := bus.Publish(context.Background(), "x", []byte("y"), events.KindBroadcast, nil)
	if err == nil {
		t.Fatal("publish after close should error")
	}
}
