package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamBus is the production durable transport. Broadcast path is
// served by a core-NATS publisher/subscriber pair (ephemeral fan-out);
// durable path uses JetStream pull consumers with explicit ack, configurable
// AckWait, MaxDeliver, and a dead-letter subject for poison messages.
//
// Construct via NewJetStreamBus. The zero value is not usable.
//
// Concurrency: every exported method is safe for concurrent use. A single
// JetStreamBus can back many Subscribe* calls; each subscription owns its
// own consumer goroutine.
type JetStreamBus struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	stream string
	logger *slog.Logger

	// ownConn reports whether nc was created by NewJetStreamBus (and must
	// be closed on Close) or passed in by the caller (who manages its
	// lifecycle independently).
	ownConn bool

	mu         sync.Mutex
	closed     bool
	subCancels []context.CancelFunc
}

// JetStreamConfig wires a JetStreamBus at construction time. All fields
// except NATSURL are optional.
type JetStreamConfig struct {
	// NATSURL is the NATS server URL (e.g. "nats://localhost:4222").
	// Ignored when Conn is non-nil.
	NATSURL string
	// Conn, when set, reuses an existing nats.Conn instead of dialing
	// NATSURL. The bus does not close Conn on Close(); the caller retains
	// ownership.
	Conn *nats.Conn
	// StreamName is the JetStream stream backing durable subjects.
	// Default "protobridge". The stream is created on first use with
	// WorkQueuePolicy retention and the SubjectPrefix("*.>" pattern by
	// default) — override via StreamSubjects when integrating with an
	// existing stream layout.
	StreamName string
	// StreamSubjects lists the subjects the stream should bind. Must be
	// set — JetStream refuses the unrestricted ">" default because it
	// would capture reserved "$JS.>" subjects. A typical production
	// value is []string{"events.>"} or a per-domain list like
	// []string{"task.>", "session.>"}. Callers who use the broadcast leg
	// on the same NATS account should keep broadcast subjects outside
	// these patterns.
	StreamSubjects []string
	// Logger receives broadcast best-effort failures, dispatch panics,
	// DLQ routing decisions, and heartbeat failures. Defaults to slog.Default().
	Logger *slog.Logger
}

// NewJetStreamBus dials NATS (or reuses cfg.Conn), ensures the JetStream
// stream exists with WorkQueuePolicy retention, and returns a Bus ready
// to publish and subscribe. Call Close to tear everything down.
func NewJetStreamBus(ctx context.Context, cfg JetStreamConfig) (*JetStreamBus, error) {
	if cfg.StreamName == "" {
		cfg.StreamName = "protobridge"
	}
	if len(cfg.StreamSubjects) == 0 {
		return nil, errors.New("events: JetStreamConfig.StreamSubjects must be non-empty (e.g. []string{\"events.>\"})")
	}

	var (
		nc      *nats.Conn
		ownConn bool
		err     error
	)
	if cfg.Conn != nil {
		nc = cfg.Conn
	} else {
		if cfg.NATSURL == "" {
			return nil, errors.New("events: JetStreamConfig needs either Conn or NATSURL")
		}
		nc, err = nats.Connect(cfg.NATSURL)
		if err != nil {
			return nil, fmt.Errorf("events: nats connect: %w", err)
		}
		ownConn = true
	}

	js, err := jetstream.New(nc)
	if err != nil {
		if ownConn {
			nc.Close()
		}
		return nil, fmt.Errorf("events: jetstream init: %w", err)
	}

	// CreateOrUpdateStream is idempotent — safe across restarts of many
	// pods racing to start the bus simultaneously.
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      cfg.StreamName,
		Subjects:  cfg.StreamSubjects,
		Retention: jetstream.WorkQueuePolicy,
		Storage:   jetstream.FileStorage,
	})
	if err != nil {
		if ownConn {
			nc.Close()
		}
		return nil, fmt.Errorf("events: create stream %q: %w", cfg.StreamName, err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &JetStreamBus{
		nc:      nc,
		js:      js,
		stream:  cfg.StreamName,
		logger:  logger,
		ownConn: ownConn,
	}, nil
}

// Publish honors Kind by routing durable to JetStream (awaits server ack)
// and broadcast to core NATS (fire-and-forget). BOTH publishes durable
// first and broadcasts only if durable succeeded.
func (b *JetStreamBus) Publish(ctx context.Context, subject string, payload []byte, kind Kind, headers map[string]string) error {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return errors.New("events: bus is closed")
	}

	switch kind {
	case KindBroadcast:
		return b.publishCore(subject, payload, headers)
	case KindDurable:
		return b.publishJetStream(ctx, subject, payload, headers)
	case KindBoth:
		if err := b.publishJetStream(ctx, subject, payload, headers); err != nil {
			return err
		}
		if err := b.publishCore(subject, payload, headers); err != nil {
			b.logger.Warn("events: broadcast leg of BOTH publish failed",
				"subject", subject, "err", err)
		}
		return nil
	default:
		return fmt.Errorf("events: unknown kind %d", kind)
	}
}

func (b *JetStreamBus) publishCore(subject string, payload []byte, headers map[string]string) error {
	msg := &nats.Msg{Subject: subject, Data: payload, Header: nats.Header{}}
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	if err := b.nc.PublishMsg(msg); err != nil {
		b.logger.Warn("events: broadcast publish failed", "subject", subject, "err", err)
	}
	return nil
}

func (b *JetStreamBus) publishJetStream(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
	msg := &nats.Msg{Subject: subject, Data: payload, Header: nats.Header{}}
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	if _, err := b.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("events: durable publish %q: %w", subject, err)
	}
	return nil
}

// SubscribeDurable creates a JetStream pull consumer named after group
// (durable), configures AckExplicit + AckWait + MaxDeliver per opts, and
// spawns a goroutine that dispatches fetched messages to h. Panics in h
// are recovered and nack'd.
func (b *JetStreamBus) SubscribeDurable(subject, group string, h Handler, opts ...DurableOption) (Subscription, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("events: bus is closed")
	}
	b.mu.Unlock()

	if group == "" {
		return nil, errors.New("events: SubscribeDurable requires a non-empty group (JetStream durable name)")
	}

	cfg := ResolveDurableConfig(subject, opts...)

	stream, err := b.js.Stream(context.Background(), b.stream)
	if err != nil {
		return nil, fmt.Errorf("events: lookup stream %q: %w", b.stream, err)
	}

	consumerName := consumerNameFor(group, subject)
	consumer, err := stream.CreateOrUpdateConsumer(context.Background(), jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.MaxDeliver,
		MaxAckPending: cfg.MaxInFlight,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("events: create consumer %q: %w", consumerName, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	b.mu.Lock()
	b.subCancels = append(b.subCancels, cancel)
	b.mu.Unlock()

	cctx, err := consumer.Consume(func(jm jetstream.Msg) {
		b.dispatch(ctx, jm, subject, cfg, h)
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("events: consume %q: %w", consumerName, err)
	}

	sub := &jsSubscription{stop: func() {
		cctx.Stop()
		cancel()
	}}
	return sub, nil
}

// SubscribeBroadcast is a plain core-NATS subscriber — ephemeral, no ack.
func (b *JetStreamBus) SubscribeBroadcast(subject string, h Handler) (Subscription, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("events: bus is closed")
	}
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.subCancels = append(b.subCancels, cancel)
	b.mu.Unlock()

	sub, err := b.nc.Subscribe(subject, func(m *nats.Msg) {
		headers := map[string]string{}
		for k, vs := range m.Header {
			if len(vs) > 0 {
				headers[k] = vs[0]
			}
		}
		msg := Message{
			Subject:    m.Subject,
			Payload:    m.Data,
			Headers:    headers,
			Ack:        func() {},
			Nack:       func() {},
			InProgress: func() error { return nil },
		}
		defer func() {
			if rec := recover(); rec != nil {
				b.logger.Error("events: broadcast handler panic",
					"subject", m.Subject, "panic", rec)
			}
		}()
		if err := h(ctx, msg); err != nil {
			b.logger.Warn("events: broadcast handler returned error",
				"subject", m.Subject, "err", err)
		}
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("events: subscribe %q: %w", subject, err)
	}

	return &jsSubscription{stop: func() {
		_ = sub.Unsubscribe()
		cancel()
	}}, nil
}

// dispatch owns the lifecycle of a single delivered message. It converts
// the JetStream message into an events.Message with ack/nack/heartbeat
// closures, runs the handler under a panic recovery, and routes exhausted
// deliveries to the DLQ when configured.
func (b *JetStreamBus) dispatch(ctx context.Context, jm jetstream.Msg, subject string, cfg DurableConfig, h Handler) {
	meta, metaErr := jm.Metadata()

	// The Ack/Nack/InProgress closures are idempotent via terminalOnce so
	// handlers that call them more than once (or call both) don't trigger
	// "message already acknowledged" errors from the JetStream client.
	var terminal sync.Once
	ack := func() { terminal.Do(func() { _ = jm.Ack() }) }
	nack := func() { terminal.Do(func() { _ = jm.Nak() }) }
	inProgress := func() error { return jm.InProgress() }

	headers := map[string]string{}
	for k, vs := range jm.Headers() {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	msg := Message{
		Subject:    subject,
		Payload:    jm.Data(),
		Headers:    headers,
		Ack:        ack,
		Nack:       nack,
		InProgress: inProgress,
	}

	defer func() {
		if rec := recover(); rec != nil {
			b.logger.Error("events: durable handler panic",
				"subject", subject, "panic", rec)
			nack()
		}
	}()

	err := h(ctx, msg)

	// Route to DLQ once MaxDeliver is exhausted (handler has nack'd or
	// errored and JetStream has already retried the allowed number of
	// times). We detect this by the delivery count matching MaxDeliver —
	// the next redelivery would exceed the cap, so publish to DLQ now and
	// Ack so JetStream stops retrying.
	if metaErr == nil && cfg.MaxDeliver > 0 && int(meta.NumDelivered) >= cfg.MaxDeliver && err != nil {
		if cfg.DeadLetterSubject != "-" {
			b.routeToDLQ(jm, subject, meta, cfg.DeadLetterSubject, err)
		}
		ack()
	}
}

// routeToDLQ publishes the poison message to the configured DLQ subject
// with headers describing why it landed there. Fire-and-forget on the
// broadcast path — DLQ is best-effort by design (losing a DLQ message is
// less bad than looping forever).
func (b *JetStreamBus) routeToDLQ(jm jetstream.Msg, origSubject string, meta *jetstream.MsgMetadata, dlqSubject string, handlerErr error) {
	dlq := &nats.Msg{
		Subject: dlqSubject,
		Data:    jm.Data(),
		Header:  nats.Header{},
	}
	for k, vs := range jm.Headers() {
		dlq.Header[k] = append([]string(nil), vs...)
	}
	dlq.Header.Set("X-Dlq-Reason", "max_deliver_exceeded")
	dlq.Header.Set("X-Dlq-Original-Subject", origSubject)
	dlq.Header.Set("X-Dlq-Attempts", strconv.FormatUint(meta.NumDelivered, 10))
	if handlerErr != nil {
		dlq.Header.Set("X-Dlq-Error", truncate(handlerErr.Error(), 512))
	}
	if _, err := b.js.PublishMsg(context.Background(), dlq); err != nil {
		b.logger.Error("events: DLQ publish failed",
			"original_subject", origSubject, "dlq_subject", dlqSubject, "err", err)
	}
}

// Close stops every subscription, then closes the NATS connection if this
// bus created it.
func (b *JetStreamBus) Close() error {
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
	if b.ownConn && b.nc != nil {
		b.nc.Close()
	}
	return nil
}

// consumerNameFor builds a durable-consumer name that's stable across
// restarts (so JetStream preserves progress) and unique per group+subject
// pair (so two different subjects in the same group don't collide).
// JetStream forbids '.' '*' '>' in durable names — we replace with '_'.
func consumerNameFor(group, subject string) string {
	raw := group + "--" + subject
	return strings.NewReplacer(".", "_", "*", "_", ">", "_").Replace(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type jsSubscription struct {
	stop func()
	once sync.Once
}

func (s *jsSubscription) Unsubscribe() error {
	s.once.Do(s.stop)
	return nil
}

// Compile-time check that *JetStreamBus satisfies Bus.
var _ Bus = (*JetStreamBus)(nil)
