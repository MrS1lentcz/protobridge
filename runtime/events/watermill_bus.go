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
//
// Returns a clear configuration error if the publisher required for the
// chosen kind is nil — this used to panic in production for callers who
// only configured one half of the bus.
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
		if b.BroadcastPublisher == nil {
			return errors.New("events: broadcast publisher not configured")
		}
		// Best-effort: log on failure, do not surface to caller.
		if err := b.BroadcastPublisher.Publish(subject, msg); err != nil {
			b.logger().Warn("events: broadcast publish failed",
				"subject", subject, "err", err)
		}
		return nil
	case KindDurable:
		if b.DurablePublisher == nil {
			return errors.New("events: durable publisher not configured")
		}
		if err := b.DurablePublisher.Publish(subject, msg); err != nil {
			return fmt.Errorf("events: durable publish %q: %w", subject, err)
		}
		return nil
	case KindBoth:
		if b.DurablePublisher == nil || b.BroadcastPublisher == nil {
			return errors.New("events: BOTH kind requires both durable and broadcast publishers")
		}
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

// SubscribeDurable registers a load-balanced consumer.
//
// IMPORTANT: Watermill's Subscriber interface is addressed by *topic only* —
// consumer groups are baked into the Subscriber instance via its config
// (e.g. NATS JetStream `DurableName`, AMQP queue name, Redis Streams
// consumer group). Passing a different group to a single shared
// DurableSubscriber does NOT load-balance the stream. To get true
// per-group load-balancing, instantiate a separate Watermill Subscriber
// per group (typically at app startup) and pass it to its own WatermillBus.
//
// Returning an error if a non-empty group is passed would force every
// caller to track this caveat; instead the bus accepts the group, logs a
// warning the first time it sees one, and proceeds with the configured
// subscriber. Tracked as v0.5+ work — a per-group Subscriber factory.
func (b *WatermillBus) SubscribeDurable(subject, group string, h Handler) (Subscription, error) {
	if group != "" {
		b.logger().Warn("events: SubscribeDurable group is informational only — see WatermillBus docs",
			"subject", subject, "group", group)
	}
	return b.subscribe(b.DurableSubscriber, subject, h)
}

// SubscribeBroadcast registers an ephemeral fan-out consumer. group is
// ignored; broadcast subscribers always receive every message.
func (b *WatermillBus) SubscribeBroadcast(subject string, h Handler) (Subscription, error) {
	return b.subscribe(b.BroadcastSubscriber, subject, h)
}

func (b *WatermillBus) subscribe(sub wmsg.Subscriber, subject string, h Handler) (Subscription, error) {
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

	ch, err := sub.Subscribe(ctx, subject)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("events: subscribe %q: %w", subject, err)
	}

	go func() {
		for m := range ch {
			b.dispatch(ctx, subject, m, h)
		}
	}()

	return &watermillSubscription{cancel: cancel}, nil
}

func (b *WatermillBus) dispatch(ctx context.Context, subject string, m *wmsg.Message, h Handler) {
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
	// Subject must be the actual subscribed topic — Watermill doesn't stamp
	// it on the Message, so we pass it through from subscribe().
	msg := Message{
		Subject: subject,
		Payload: m.Payload,
		Headers: headers,
		Ack:     func() { m.Ack() },
		Nack:    func() { m.Nack() },
	}
	// Contract per Bus.Handler doc: the handler is responsible for calling
	// Ack/Nack on msg before returning. dispatch only logs a non-nil error;
	// it does NOT auto-Nack — the generated subscriber helpers already do
	// that, and double-Nack on Watermill messages can panic depending on
	// the backend.
	if err := h(ctx, msg); err != nil {
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
