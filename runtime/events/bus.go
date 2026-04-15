// Package events is the runtime support for protobridge-events. It defines
// a transport-agnostic Bus interface plus two implementations: a Watermill-
// backed one (ephemeral broadcast + in-memory / legacy durable for tests)
// and a JetStream-backed one (production durable path with at-least-once,
// heartbeats, DLQ, and configurable redelivery).
//
// Generated typed Emit*/Subscribe* helpers (from protoc-gen-events-go)
// delegate to a Bus instance — application code never touches transport
// types directly.
package events

import (
	"context"
	"time"
)

// Kind describes how an event flows through the bus. Mirrors the proto
// EventKind enum (BROADCAST / DURABLE / BOTH).
type Kind int

const (
	KindUnspecified Kind = iota
	// KindBroadcast routes via the ephemeral fan-out path (core NATS,
	// Redis Pub/Sub, fanout exchange). Subscribers that are offline miss
	// messages; no replay. Best-effort delivery.
	KindBroadcast
	// KindDurable routes via the persistent at-least-once path. In
	// production back this with a JetStreamBus; in tests an InMemoryBus
	// is fine. Failed handlers cause backend-defined redelivery.
	// Publish failures bubble up to the caller.
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
	// opts tune per-subscription durability parameters (ack deadline,
	// redelivery cap, in-flight fan-out, dead-letter subject). See
	// DurableOption for defaults. The Watermill-backed Bus accepts opts
	// for API consistency but only JetStreamBus enforces them.
	SubscribeDurable(subject, group string, h Handler, opts ...DurableOption) (Subscription, error)

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
	// InProgress extends the ack deadline for long-running handlers.
	// Generated Subscribe* wrappers call this on a timer (half of the
	// configured AckWait) so slow handlers don't trigger redelivery while
	// the process is still alive and making progress.
	//
	// On transports that don't expose a deadline (broadcast paths, the
	// Watermill-backed durable path kept for tests) InProgress is a no-op
	// returning nil — callers don't need to branch by transport.
	InProgress func() error
}

// Subscription is the handle returned by SubscribeDurable / SubscribeBroadcast.
// Calling Unsubscribe stops the underlying consumer goroutine and releases
// any per-subscription backend resources.
type Subscription interface {
	Unsubscribe() error
}

// DurableOption tunes a durable subscription's delivery semantics. Only
// JetStreamBus enforces these; other implementations accept the options
// for API consistency and ignore them (optionally logging a warning).
type DurableOption func(*DurableConfig)

// DurableConfig is the resolved configuration used by a durable subscription.
// Fields left at zero-value fall back to the documented defaults when the
// subscription starts.
type DurableConfig struct {
	// AckWait is how long the backend waits for Ack/Nack/InProgress before
	// treating the delivery as failed and redelivering. Default 30s.
	AckWait time.Duration
	// MaxDeliver caps redelivery attempts. After this many attempts the
	// message is routed to DeadLetterSubject. Default 5. WithMaxDeliver
	// rejects non-positive values (defaults stay), so to truly disable
	// the DLQ hop set DeadLetterSubject = "-" via WithDeadLetterSubject.
	MaxDeliver int
	// MaxInFlight bounds how many messages the consumer may have unacked
	// at once. Default 1 — serial per subscriber instance, which is the
	// safest semantics for side-effectful handlers. Increase when handlers
	// are idempotent and latency-bound.
	MaxInFlight int
	// DeadLetterSubject is where messages go after MaxDeliver is exceeded
	// or a handler nacks permanently. Default "<subject>.dlq". Set to "-"
	// to drop the DLQ hop entirely.
	DeadLetterSubject string
}

// WithAckWait overrides the ack-deadline default. Must be > 0; zero leaves
// the default in place. Typical production values: 30s for RPC-style
// handlers, minutes for I/O-heavy ones (builds, LLM calls).
func WithAckWait(d time.Duration) DurableOption {
	return func(c *DurableConfig) {
		if d > 0 {
			c.AckWait = d
		}
	}
}

// WithMaxDeliver overrides the redelivery-attempt cap. A value <= 0 leaves
// the default (5) in place; pass a large number to effectively disable
// the DLQ hop without giving up the option.
func WithMaxDeliver(n int) DurableOption {
	return func(c *DurableConfig) {
		if n > 0 {
			c.MaxDeliver = n
		}
	}
}

// WithMaxInFlight overrides the per-subscriber unacked-message ceiling.
// The default of 1 serializes handlers per instance — the conservative
// choice because most handlers are side-effectful. Raise it only when
// handlers are idempotent and you need higher throughput per subscriber.
func WithMaxInFlight(n int) DurableOption {
	return func(c *DurableConfig) {
		if n > 0 {
			c.MaxInFlight = n
		}
	}
}

// WithDeadLetterSubject overrides the default dead-letter subject
// ("<subject>.dlq"). Pass "-" to drop the DLQ hop entirely (messages that
// exhaust MaxDeliver are simply acknowledged and discarded).
func WithDeadLetterSubject(subject string) DurableOption {
	return func(c *DurableConfig) {
		c.DeadLetterSubject = subject
	}
}

// ResolveDurableConfig applies opts on top of defaults. Exposed so custom
// Bus implementations can honor the same option set without re-deriving
// the defaults table.
func ResolveDurableConfig(subject string, opts ...DurableOption) DurableConfig {
	c := DurableConfig{
		AckWait:     30 * time.Second,
		MaxDeliver:  5,
		MaxInFlight: 1,
	}
	for _, o := range opts {
		o(&c)
	}
	if c.DeadLetterSubject == "" {
		c.DeadLetterSubject = subject + ".dlq"
	}
	return c
}
