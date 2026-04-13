// Package events is the runtime support for protobridge-events. It defines
// a transport-agnostic Bus interface plus a Watermill-backed implementation
// that lets users plug in any of Watermill's 10+ Pub/Sub backends (NATS,
// Redis, RabbitMQ, Kafka, GCP Pub/Sub, AWS SQS/SNS, Google Pub/Sub,
// gochannel for in-process tests, ...).
//
// Generated typed Emit*/Subscribe* helpers (from protoc-gen-events-go)
// delegate to a Bus instance — application code never touches Watermill
// types directly.
package events

import "context"

// Kind describes how an event flows through the bus. Mirrors the proto
// EventKind enum (BROADCAST / DURABLE / BOTH).
type Kind int

const (
	KindUnspecified Kind = iota
	// KindBroadcast routes via the ephemeral fan-out path (core NATS,
	// Redis Pub/Sub, fanout exchange). Subscribers that are offline miss
	// messages; no replay. Best-effort delivery.
	KindBroadcast
	// KindDurable routes via the persistent at-least-once path
	// (NATS JetStream, Redis Streams, durable AMQP queues). Failed
	// handlers cause backend-defined redelivery. Strictly required —
	// publish failures bubble up to the caller.
	KindDurable
	// KindBoth publishes to durable first; on success, also publishes to
	// broadcast as best-effort. Subscribers must be idempotent because
	// they may see the same event from both paths.
	KindBoth
)

// Bus is the transport contract. Generated typed code calls Publish for the
// emit side and SubscribeDurable / SubscribeBroadcast for the consume side.
//
// All implementations must be safe for concurrent use.
type Bus interface {
	// Publish sends payload to subject with the given kind semantics.
	//
	//   - Broadcast: best-effort, never returns an error for delivery loss.
	//   - Durable:   at-least-once, returns the underlying transport error.
	//   - Both:      durable first (fail = error returned), broadcast best-effort
	//                after (fail = logged via the configured Logger, ignored).
	//
	// headers are passed through the wire (typically used for trace context
	// and content-type). Implementations may add their own metadata but must
	// not strip caller-supplied keys.
	Publish(ctx context.Context, subject string, payload []byte, kind Kind, headers map[string]string) error

	// SubscribeDurable creates a load-balanced at-least-once subscription.
	// Multiple subscribers in the same group split the message stream. The
	// handler MUST call msg.Ack() on success or msg.Nack() on failure
	// before returning — those are the explicit signals the backend uses
	// to commit / redeliver. Returning a non-nil error is logged by the
	// runtime but does NOT auto-Ack/Nack on the handler's behalf
	// (double-Ack/Nack panics on some backends).
	//
	// Note on the group parameter: Watermill addresses subscribers by
	// topic only. Real per-group load-balancing requires a separate
	// Subscriber instance per group at app startup; today's WatermillBus
	// accepts the group, logs an info-level warning, and proceeds with
	// the configured DurableSubscriber. See WatermillBus docs.
	SubscribeDurable(subject, group string, h Handler) (Subscription, error)

	// SubscribeBroadcast creates an ephemeral fan-out subscription. Every
	// subscriber gets every message; missed messages while offline are not
	// redelivered. Ack/Nack are no-ops on broadcast subjects.
	SubscribeBroadcast(subject string, h Handler) (Subscription, error)

	// Close stops all subscriptions and releases backend resources. In-flight
	// handlers are allowed to finish (drain) before Close returns. After
	// Close any further Publish/Subscribe call returns an error.
	Close() error
}

// Handler processes a delivered message. Delivery state is driven by
// explicit msg.Ack() / msg.Nack() calls — the return value is informational
// only (logged by the bus runtime). The handler MUST call exactly one of
// Ack/Nack before returning on durable subscriptions; broadcast Ack/Nack
// are no-ops.
type Handler func(ctx context.Context, msg Message) error

// Message is a delivered event. Payload is the raw proto wire bytes; the
// generated typed Subscribe* helper unmarshals it before calling user code.
type Message struct {
	Subject string
	Payload []byte
	Headers map[string]string

	// Ack reports successful processing. Idempotent. No-op for broadcast.
	Ack func()
	// Nack signals failure. The backend decides retry policy. Idempotent.
	// No-op for broadcast.
	Nack func()
}

// Subscription is the handle returned by SubscribeDurable / SubscribeBroadcast.
// Calling Unsubscribe stops the underlying consumer goroutine and releases
// any per-subscription backend resources.
type Subscription interface {
	Unsubscribe() error
}
