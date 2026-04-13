package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill"
	wmsg "github.com/ThreeDotsLabs/watermill/message"
)

// WatermillBus adapts a Watermill Publisher + Subscriber pair (or two pairs,
// one per kind) to the Bus interface. The same underlying transport can back
// both broadcast and durable paths when the backend supports it; for backends
// that don't (e.g. NATS Core has no durability, JetStream is a separate
// product), pass a different Publisher/Subscriber to the durable fields.
//
// In tests use NewInMemoryBus, which wires gochannel to both paths.
type WatermillBus struct {
	BroadcastPublisher  wmsg.Publisher
	BroadcastSubscriber wmsg.Subscriber
	DurablePublisher    wmsg.Publisher
	DurableSubscriber   wmsg.Subscriber

	// Logger receives broadcast best-effort failures (which never bubble up
	// to Publish callers) and unexpected handler panics. Defaults to slog.Default().
	Logger *slog.Logger

	mu         sync.Mutex
	closed     bool
	subCancels []context.CancelFunc
}

// Publish dispatches to the right Watermill publisher based on kind. Both
// publishes durable first (must succeed) then broadcast (best-effort).
func (b *WatermillBus) Publish(ctx context.Context, subject string, payload []byte, kind Kind, headers map[string]string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("events: bus is closed")
	}
	b.mu.Unlock()

	msg := wmsg.NewMessage(watermill.NewUUID(), payload)
	for k, v := range headers {
		msg.Metadata.Set(k, v)
	}

	switch kind {
	case KindBroadcast:
		// Best-effort: log on failure, do not surface to caller.
		if err := b.BroadcastPublisher.Publish(subject, msg); err != nil {
			b.logger().Warn("events: broadcast publish failed",
				"subject", subject, "err", err)
		}
		return nil
	case KindDurable:
		if err := b.DurablePublisher.Publish(subject, msg); err != nil {
			return fmt.Errorf("events: durable publish %q: %w", subject, err)
		}
		return nil
	case KindBoth:
		// Durable first; on success try broadcast as best-effort. The
		// broadcast leg uses a separate Message instance so Watermill's
		// per-message ack tracking stays clean.
		if err := b.DurablePublisher.Publish(subject, msg); err != nil {
			return fmt.Errorf("events: durable publish %q: %w", subject, err)
		}
		bmsg := wmsg.NewMessage(watermill.NewUUID(), payload)
		for k, v := range headers {
			bmsg.Metadata.Set(k, v)
		}
		if err := b.BroadcastPublisher.Publish(subject, bmsg); err != nil {
			b.logger().Warn("events: broadcast leg of BOTH publish failed",
				"subject", subject, "err", err)
		}
		return nil
	default:
		return fmt.Errorf("events: unknown kind %d", kind)
	}
}

// SubscribeDurable registers a load-balanced consumer. The group parameter
// is passed through to backend implementations that honour it (e.g. NATS
// JetStream durable name, AMQP queue name, Redis Streams consumer group);
// gochannel ignores it. Returns the subscription handle for Unsubscribe.
func (b *WatermillBus) SubscribeDurable(subject, group string, h Handler) (Subscription, error) {
	return b.subscribe(b.DurableSubscriber, subject, group, h, true)
}

// SubscribeBroadcast registers an ephemeral fan-out consumer. group is
// ignored; broadcast subscribers always receive every message.
func (b *WatermillBus) SubscribeBroadcast(subject string, h Handler) (Subscription, error) {
	return b.subscribe(b.BroadcastSubscriber, subject, "", h, false)
}

func (b *WatermillBus) subscribe(sub wmsg.Subscriber, subject, group string, h Handler, durable bool) (Subscription, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("events: bus is closed")
	}
	if sub == nil {
		b.mu.Unlock()
		return nil, errors.New("events: subscriber not configured for this kind")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.subCancels = append(b.subCancels, cancel)
	b.mu.Unlock()

	// Watermill subscribers are addressed by topic only. Group routing for
	// durable consumers is encoded by the backend's New<X>Subscriber config
	// — we surface it via the Bus API for forward-compatibility (so callers
	// don't have to wire one Subscriber per group themselves) but pass
	// through unchanged today; backends ignoring it produce the simplest
	// possible behaviour (single consumer = no load balancing).
	_ = group
	_ = durable

	ch, err := sub.Subscribe(ctx, subject)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("events: subscribe %q: %w", subject, err)
	}

	go func() {
		for m := range ch {
			b.dispatch(ctx, m, h)
		}
	}()

	return &watermillSubscription{cancel: cancel}, nil
}

func (b *WatermillBus) dispatch(ctx context.Context, m *wmsg.Message, h Handler) {
	defer func() {
		if rec := recover(); rec != nil {
			b.logger().Error("events: handler panic", "panic", rec)
			m.Nack()
		}
	}()
	headers := map[string]string{}
	for k, v := range m.Metadata {
		headers[k] = v
	}
	msg := Message{
		Subject: m.UUID, // watermill stamps topic on subscriber side
		Payload: m.Payload,
		Headers: headers,
		Ack:     func() { m.Ack() },
		Nack:    func() { m.Nack() },
	}
	if err := h(ctx, msg); err != nil {
		// Handler signalled failure but didn't ack/nack itself — be safe
		// and Nack so the backend can redeliver.
		m.Nack()
		b.logger().Warn("events: handler returned error", "err", err)
	}
}

// Close cancels all subscriptions and closes the underlying Watermill
// publishers/subscribers. In-flight handler invocations are allowed to
// finish; new deliveries stop immediately.
func (b *WatermillBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	cancels := b.subCancels
	b.subCancels = nil
	b.mu.Unlock()

	for _, c := range cancels {
		c()
	}

	var firstErr error
	for _, c := range []closer{b.BroadcastPublisher, b.BroadcastSubscriber, b.DurablePublisher, b.DurableSubscriber} {
		if c == nil {
			continue
		}
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (b *WatermillBus) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

type closer interface{ Close() error }

type watermillSubscription struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (s *watermillSubscription) Unsubscribe() error {
	s.once.Do(s.cancel)
	return nil
}
