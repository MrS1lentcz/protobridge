package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
// Run blocks until ctx is cancelled or the source decides to give up.
// Returning a non-nil error causes the hub to re-invoke Run after an
// exponential backoff (see BroadcastConfig.SourceRetry{Initial,Max}); the
// backoff resets to Initial after Run delivers at least one frame, so a
// long-lived source that occasionally drops survives transient failures
// without compounding delay. Implementations may still handle reconnects
// internally as an optimization, but are no longer required to.
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

	// TicketStore, when non-nil, enables ticket-based auth for SSE
	// clients that can't send headers (browsers' EventSource). A request
	// arriving with ?ticket=<t> bypasses PrincipalLabels and uses the
	// labels bound to the ticket at issuance time. The ticket is redeemed
	// (consumed) on use. When the param is absent, PrincipalLabels runs
	// as normal — both paths coexist.
	//
	// Pair with NewTicketIssuer mounted on a path covered by your normal
	// auth middleware. Usually the generator wires both sides for you.
	TicketStore TicketStore

	// TicketParam is the query-parameter name carrying the ticket.
	// Defaults to "ticket".
	TicketParam string

	// HeartbeatInterval controls how often the SSE handler emits a
	// comment-line keepalive (":ping\n\n"). Keeps idle connections alive
	// through proxies/load-balancers with short idle timeouts. Defaults
	// to 15s. WebSocket connections rely on the library's own ping
	// handling and ignore this setting.
	HeartbeatInterval time.Duration

	// ClientBuffer is the per-WS-client outbound queue depth. When a slow
	// client fills the queue, the oldest pending frame is dropped (drop-
	// oldest, not drop-newest — the latest UX nudge is the most useful
	// one) and a counter is incremented. Defaults to 64.
	ClientBuffer int

	// SourceRetryInitial / SourceRetryMax control the exponential backoff
	// the hub applies when Source.Run returns a non-nil error. After each
	// failure the hub waits backoff (starting at Initial, doubling up to
	// Max) and re-invokes Run. The backoff resets to Initial as soon as
	// Run delivers a frame, so a source that survives for a while before
	// failing reconnects fast on the next blip instead of inheriting the
	// previous outage's cap.
	//
	// Defaults: 1s initial, 30s max — safe for the typical container
	// restart-ordering race ("gateway booted before backend"). Set
	// SourceRetryMax to a negative value to disable retry entirely (the
	// hub logs the error and stops the source — pre-retry behavior, useful
	// for sources that implement their own reconnect or for tests).
	SourceRetryInitial time.Duration
	SourceRetryMax     time.Duration

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

	// ctx is the lifetime context passed to NewBroadcastHub. Cancellation
	// stops the source goroutine, terminates the dispatch loop, and
	// disconnects every active WS client (each connection's ctx is linked
	// in ServeHTTP).
	ctx context.Context

	mu      sync.Mutex
	clients map[*hubClient]struct{}
}

type hubClient struct {
	out             chan []byte
	principalLabels map[string]string
	// dropped is incremented by the hub's dispatch goroutine and read by
	// ServeHTTP on disconnect — atomic to keep the race detector happy.
	dropped atomic.Uint64
}

// NewBroadcastHub starts the source goroutine in the background. The hub is
// returned ready to serve HTTP — mount it at the WS route. The source runs
// until ctx is cancelled; on cancel all clients are disconnected.
//
// When Source.Run returns a non-nil error the hub logs it and re-invokes
// Run after an exponential backoff (BroadcastConfig.SourceRetry{Initial,Max},
// defaulting to 1s/30s; reset to Initial after Run delivers a frame). This
// keeps the hub alive across the typical container-startup race where the
// gateway boots before the backend is ready to accept the broadcast stream.
// Set SourceRetryMax negative to opt out of retry — the hub then logs and
// stops, leaving connected clients live but quiet (pre-retry behavior).
func NewBroadcastHub(ctx context.Context, cfg BroadcastConfig) *BroadcastHub {
	if ctx == nil {
		// A nil ctx would panic the first time linkHubLifetime selects on
		// h.ctx.Done(). Downgrading to Background keeps the hub usable —
		// the caller loses the ability to tear it down cooperatively, but
		// that's a misuse, not a crash worth propagating into every
		// request goroutine.
		ctx = context.Background()
	}
	if cfg.ClientBuffer <= 0 {
		cfg.ClientBuffer = 64
	}
	if cfg.Matcher == nil {
		cfg.Matcher = DefaultLabelMatcher
	}
	if cfg.SourceRetryInitial <= 0 {
		cfg.SourceRetryInitial = 1 * time.Second
	}
	// == 0 (not <= 0) so callers can opt out with a negative value.
	if cfg.SourceRetryMax == 0 {
		cfg.SourceRetryMax = 30 * time.Second
	}
	h := &BroadcastHub{
		cfg:     cfg,
		ctx:     ctx,
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
	dispatchDone := make(chan struct{})

	// Reader goroutine: dispatch every frame to every connected client.
	// Exits when ctx is cancelled OR `in` is closed (which happens after
	// Source.Run returns) — without the close, a Source that exits while
	// ctx is still live would leak this goroutine forever.
	go func() {
		defer close(dispatchDone)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-in:
				if !ok {
					return
				}
				h.dispatch(f)
			}
		}
	}()

	h.runSource(ctx, in)
	close(in)
	<-dispatchDone
}

// runSource drives Source.Run with exponential-backoff retry. Each iteration
// gets a fresh per-attempt channel; a forwarder copies frames into the hub's
// dispatch channel and tracks first delivery so a successful run resets the
// backoff. Returns when ctx is cancelled, when retry is disabled and Run
// errors, or when Run returns nil (clean source-driven stop).
func (h *BroadcastHub) runSource(ctx context.Context, in chan<- BroadcastFrame) {
	backoff := h.cfg.SourceRetryInitial
	retryDisabled := h.cfg.SourceRetryMax < 0

	for {
		srcOut := make(chan BroadcastFrame, 64)
		var delivered atomic.Bool
		forwardDone := make(chan struct{})
		go func() {
			defer close(forwardDone)
			for f := range srcOut {
				delivered.Store(true)
				select {
				case in <- f:
				case <-ctx.Done():
					// Drain remaining frames so Source.Run's send doesn't
					// block forever waiting for srcOut to be read.
					for range srcOut {
					}
					return
				}
			}
		}()

		err := h.cfg.Source.Run(ctx, srcOut)
		close(srcOut)
		<-forwardDone

		if ctx.Err() != nil {
			return
		}
		// nil err without ctx cancel = source decided to stop cleanly.
		// Honor that — don't restart it in a tight loop.
		if err == nil {
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		if retryDisabled {
			h.logger().Error("events: broadcast source terminated", "err", err)
			return
		}

		if delivered.Load() {
			backoff = h.cfg.SourceRetryInitial
		}
		h.logger().Error("events: broadcast source terminated, will retry",
			"err", err, "backoff", backoff)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > h.cfg.SourceRetryMax {
			backoff = h.cfg.SourceRetryMax
		}
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
				c.dropped.Add(1)
			default:
			}
			select {
			case c.out <- envelope:
			default:
				c.dropped.Add(1)
			}
		}
	}
}

// ServeHTTP content-negotiates between SSE and WebSocket, resolves the
// caller's principal labels (via ticket redemption or PrincipalLabels),
// and hands the connection off to the matching transport handler. Auth
// failures land on a plain HTTP response code before any upgrade.
func (h *BroadcastHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Marshal == nil {
		http.Error(w, "broadcast handler not configured", http.StatusInternalServerError)
		return
	}

	principalLabels, ok := h.resolvePrincipal(w, r)
	if !ok {
		return
	}

	if isSSERequest(r) {
		h.serveSSE(w, r, principalLabels)
		return
	}
	h.serveWebSocket(w, r, principalLabels)
}

// resolvePrincipal runs the ticket redemption first (if configured and the
// query param is present), then falls back to PrincipalLabels. Returns
// false after writing an HTTP error response — caller should return.
func (h *BroadcastHub) resolvePrincipal(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	if h.cfg.TicketStore != nil {
		param := h.cfg.TicketParam
		if param == "" {
			param = "ticket"
		}
		if ticket := r.URL.Query().Get(param); ticket != "" {
			labels, err := h.cfg.TicketStore.Redeem(r.Context(), ticket)
			if err != nil {
				h.logger().Warn("events: broadcast ticket redemption failed",
					"remote", r.RemoteAddr, "err", err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return nil, false
			}
			return labels, true
		}
	}
	if h.cfg.PrincipalLabels != nil {
		pl, err := h.cfg.PrincipalLabels(r)
		if err != nil {
			h.logger().Warn("events: broadcast principal labels resolution failed",
				"remote", r.RemoteAddr, "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return nil, false
		}
		return pl, true
	}
	return nil, true
}

// isSSERequest returns true if the Accept header advertises text/event-
// stream. EventSource sends exactly this; tooling like curl -NH
// "Accept: text/event-stream" also works.
func isSSERequest(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		if strings.Contains(v, "text/event-stream") {
			return true
		}
	}
	return false
}

func (h *BroadcastHub) register(principalLabels map[string]string) *hubClient {
	c := &hubClient{
		out:             make(chan []byte, h.cfg.ClientBuffer),
		principalLabels: principalLabels,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	if h.cfg.onSubscribed != nil {
		h.cfg.onSubscribed()
	}
	return c
}

func (h *BroadcastHub) unregister(c *hubClient, remote string) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	if dropped := c.dropped.Load(); dropped > 0 {
		h.logger().Info("events: broadcast client disconnected with dropped frames",
			"remote", remote, "dropped", dropped)
	}
}

// linkHubLifetime wires the hub's ctx into the per-request cancel so
// shutting down the hub (cancelling the ctx passed to NewBroadcastHub)
// terminates every active connection instead of leaving them hanging
// until r.Context() expires on its own.
func (h *BroadcastHub) linkHubLifetime(ctx context.Context, cancel context.CancelFunc) {
	go func() {
		select {
		case <-h.ctx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
}

func (h *BroadcastHub) serveWebSocket(w http.ResponseWriter, r *http.Request, principalLabels map[string]string) {
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
	h.linkHubLifetime(ctx, cancel)

	c := h.register(principalLabels)
	defer h.unregister(c, r.RemoteAddr)

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

// serveSSE implements the text/event-stream branch. Each enqueued
// envelope becomes one `event: message\ndata: <json>\n\n` block; a
// heartbeat comment is emitted on idle to keep proxies from closing the
// connection.
//
// Origin enforcement on SSE is delegated to upstream CORS middleware —
// EventSource obeys CORS, unlike raw WebSocket handshakes (which is why
// OriginPatterns exists for the WS path).
func (h *BroadcastHub) serveSSE(w http.ResponseWriter, r *http.Request, principalLabels map[string]string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx respects this; harmless elsewhere).
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	h.linkHubLifetime(ctx, cancel)

	c := h.register(principalLabels)
	defer h.unregister(c, r.RemoteAddr)

	heartbeat := h.cfg.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	// Per-write deadline so a stalled client (peer stopped reading) can't
	// pin this goroutine on a TCP send buffer. ctx cancellation alone
	// does not unblock net/http writes. Mirrors the 10s bound on the
	// WebSocket path.
	rc := http.NewResponseController(w)
	const writeTimeout = 10 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-c.out:
			_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := w.Write([]byte(":ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
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
