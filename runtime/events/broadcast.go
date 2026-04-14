package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// BroadcastFrame is one event delivered from a BroadcastSource to the hub.
// Payload is the raw proto-encoded event bytes; Labels carry whatever
// per-event metadata the source extracted (typically populated by the
// backend bus→stream adapter from publish headers).
type BroadcastFrame struct {
	Subject string
	Payload []byte
	Labels  map[string]string
}

// BroadcastSource produces a stream of frames the hub fans out to WS clients.
// One Source backs one BroadcastHub which backs one or more WS endpoints.
//
// Run blocks until ctx is cancelled or the source decides to give up
// permanently. Implementations should handle transient errors internally
// (reconnect, backoff). Returning a non-nil error tears the hub down — use
// it only for programmer errors (misconfigured client, etc.). For UX
// broadcast semantics, prefer logging and reconnecting over returning.
type BroadcastSource interface {
	Run(ctx context.Context, out chan<- BroadcastFrame) error
}

// EnvelopeMarshaler turns a frame's raw payload into the JSON envelope the
// WS endpoint sends to its clients. The events plugin emits a per-package
// implementation that knows how to unmarshal each subject's payload into the
// matching proto and re-marshal as protojson; runtime stays agnostic about
// the message catalog.
//
// Returning a non-nil error causes the message to be dropped (the WS stream
// stays alive — one bad event does not disconnect the client) and the
// failure is logged via BroadcastConfig.Logger (or slog.Default when unset).
type EnvelopeMarshaler func(subject string, payload []byte, labels map[string]string) ([]byte, error)

// BroadcastConfig wires a BroadcastHub. Source is the single shared event
// stream (typically one gRPC server-stream from the backend); Marshal turns
// each frame into the JSON envelope clients receive.
type BroadcastConfig struct {
	Source  BroadcastSource
	Marshal EnvelopeMarshaler

	// PrincipalLabels resolves the labels carried by an inbound WS
	// connection's authenticated user (e.g. tenant_id, role, ...).
	// Combined with Matcher, this is the security boundary: the hub
	// only forwards events whose labels match the principal's.
	// Returning an error closes the connection (typical use: auth
	// rejected). Nil/empty map means "no labels" — events with non-empty
	// labels will be filtered out by DefaultLabelMatcher.
	//
	// When PrincipalLabels itself is nil, every connection is treated as
	// having no labels — suitable for single-tenant deployments where
	// every authenticated user sees every event.
	PrincipalLabels func(r *http.Request) (map[string]string, error)

	// Matcher decides per delivered frame whether to forward it to a given
	// client given its principal's labels. Defaults to DefaultLabelMatcher
	// (Kubernetes-style label selector semantics).
	Matcher LabelMatcher

	// OriginPatterns restricts which Origin headers are accepted on the
	// inbound WS handshake. Empty means "same-origin only" — the secure
	// default. To allow specific browser origins, list them explicitly.
	// To allow ALL origins (cross-site WS hijacking risk), set
	// SkipOriginVerify=true; preferred only behind an upstream proxy that
	// already enforces origin policy.
	OriginPatterns   []string
	SkipOriginVerify bool

	// ClientBuffer is the per-WS-client outbound queue depth. When a slow
	// client fills the queue, the oldest pending frame is dropped (drop-
	// oldest, not drop-newest — the latest UX nudge is the most useful
	// one) and a counter is incremented. Defaults to 64.
	ClientBuffer int

	// Logger receives source/marshal failures and per-client drop counts.
	// Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// onSubscribed is a test-only readiness hook fired after a client has
	// been registered on the hub. Lets deterministic tests skip warm-up
	// sleeps. Intentionally unexported.
	onSubscribed func()
}

// BroadcastHub holds one BroadcastSource open and fans every received frame
// out to all currently-connected WS clients. Each WS connection becomes one
// client with its own per-principal label filter and bounded outbound queue.
//
// Construct via NewBroadcastHub at service startup; mount as http.Handler at
// the route the WS endpoint should live at. Cancel the ctx passed to
// NewBroadcastHub to stop the source and disconnect all clients.
type BroadcastHub struct {
	cfg BroadcastConfig

	mu      sync.Mutex
	clients map[*hubClient]struct{}
}

type hubClient struct {
	out             chan []byte
	principalLabels map[string]string
	dropped         uint64
}

// NewBroadcastHub starts the source goroutine in the background. The hub is
// returned ready to serve HTTP — mount it at the WS route. The source runs
// until ctx is cancelled; on cancel all clients are disconnected.
//
// When source.Run returns an error the hub logs it and the source stops; new
// clients can still attach but will receive no events. Source implementations
// that should survive transient backend failures must implement reconnect
// internally before returning.
func NewBroadcastHub(ctx context.Context, cfg BroadcastConfig) *BroadcastHub {
	if cfg.ClientBuffer <= 0 {
		cfg.ClientBuffer = 64
	}
	if cfg.Matcher == nil {
		cfg.Matcher = DefaultLabelMatcher
	}
	h := &BroadcastHub{
		cfg:     cfg,
		clients: make(map[*hubClient]struct{}),
	}
	go h.run(ctx)
	return h
}

func (h *BroadcastHub) logger() *slog.Logger {
	if h.cfg.Logger != nil {
		return h.cfg.Logger
	}
	return slog.Default()
}

func (h *BroadcastHub) run(ctx context.Context) {
	if h.cfg.Source == nil {
		h.logger().Error("events: broadcast hub started with nil Source — no events will be delivered")
		return
	}
	in := make(chan BroadcastFrame, 64)

	// Reader goroutine: dispatch every frame to every connected client.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-in:
				h.dispatch(f)
			}
		}
	}()

	if err := h.cfg.Source.Run(ctx, in); err != nil && !errors.Is(err, context.Canceled) {
		h.logger().Error("events: broadcast source terminated", "err", err)
	}
}

func (h *BroadcastHub) dispatch(f BroadcastFrame) {
	h.mu.Lock()
	clients := make([]*hubClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		// Per-client security filter: drop frames whose labels don't match
		// the principal's. Filter runs at delivery, not subscription —
		// labels are per-frame, not per-subject.
		if !h.cfg.Matcher(c.principalLabels, f.Labels) {
			continue
		}
		envelope, err := h.cfg.Marshal(f.Subject, f.Payload, f.Labels)
		if err != nil {
			h.logger().Warn("events: broadcast marshal failed",
				"subject", f.Subject, "err", err)
			continue
		}
		// Drop-oldest: if queue is full, evict the head and enqueue the
		// new frame. UX semantics — newest is most useful. Counter lets
		// operators see persistent slowness per-client (logged on close).
		select {
		case c.out <- envelope:
		default:
			select {
			case <-c.out:
				c.dropped++
			default:
			}
			select {
			case c.out <- envelope:
			default:
				c.dropped++
			}
		}
	}
}

// ServeHTTP upgrades the request to WebSocket and registers it as a hub
// client. Per-principal labels are resolved before the upgrade so auth
// failures land on a plain HTTP response code.
func (h *BroadcastHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Marshal == nil {
		http.Error(w, "broadcast handler not configured", http.StatusInternalServerError)
		return
	}

	var principalLabels map[string]string
	if h.cfg.PrincipalLabels != nil {
		pl, err := h.cfg.PrincipalLabels(r)
		if err != nil {
			h.logger().Warn("events: broadcast principal labels resolution failed",
				"remote", r.RemoteAddr, "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		principalLabels = pl
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:     h.cfg.OriginPatterns,
		InsecureSkipVerify: h.cfg.SkipOriginVerify,
	})
	if err != nil {
		return // websocket library already wrote the response
	}
	defer ws.CloseNow() //nolint:errcheck

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c := &hubClient{
		out:             make(chan []byte, h.cfg.ClientBuffer),
		principalLabels: principalLabels,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		if c.dropped > 0 {
			h.logger().Info("events: broadcast client disconnected with dropped frames",
				"remote", r.RemoteAddr, "dropped", c.dropped)
		}
	}()

	if h.cfg.onSubscribed != nil {
		h.cfg.onSubscribed()
	}

	// Reader goroutine: detect client disconnect / ping.
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-c.out:
			// Bound write time so a stuck client can't pin a goroutine
			// indefinitely — context cancel handles the upper bound but
			// websocket.Write itself may block on TCP send buffers.
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Write(wctx, websocket.MessageText, payload)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

// JSONEnvelope is the canonical wire shape emitted to broadcast WS clients.
// The events plugin's generated marshaler produces this struct per delivered
// frame; exposed here so handwritten clients (or custom marshalers) can match
// the contract.
//
// Labels piggyback on every envelope so browser clients can apply per-screen
// UX filtering on top of the server-side auth filter (e.g. "show only events
// for the project the user is currently viewing"). omitempty keeps the wire
// compatible with consumers written against the pre-labels envelope shape.
type JSONEnvelope struct {
	Subject string            `json:"subject"`
	Labels  map[string]string `json:"labels,omitempty"`
	Event   json.RawMessage   `json:"event"`
}

// MarshalJSONEnvelope wraps an already-encoded event payload in the canonical
// envelope. Labels are attached when non-empty.
func MarshalJSONEnvelope(subject string, event json.RawMessage, labels map[string]string) ([]byte, error) {
	return json.Marshal(JSONEnvelope{Subject: subject, Labels: labels, Event: event})
}
