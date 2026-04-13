package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
)

// EnvelopeMarshaler turns a raw Message off the bus into the JSON envelope
// the broadcast WS endpoint sends to its clients. The events plugin emits a
// per-package implementation that knows how to unmarshal each subject's
// payload into the matching proto and re-marshal as protojson; runtime stays
// agnostic about the message catalog.
//
// Returning a non-nil error causes the message to be dropped (the WS stream
// stays alive — one bad event does not disconnect the client) and the
// failure is logged via BroadcastConfig.Logger (or slog.Default when unset).
type EnvelopeMarshaler func(subject string, payload []byte, headers map[string]string) ([]byte, error)

// BroadcastConfig wires a BroadcastHandler. Subjects lists every PUBLIC
// broadcast subject for one logical event group (typically all PUBLIC
// events from one Go package). Marshal turns each delivered message into
// the envelope written to the WS client. Bus is the underlying transport.
type BroadcastConfig struct {
	Bus      Bus
	Subjects []string
	Marshal  EnvelopeMarshaler

	// PrincipalLabels resolves the labels carried by the inbound WS
	// connection's authenticated user (e.g. tenant_id, role, ...).
	// Combined with Matcher, this is the security boundary: the broadcast
	// handler only forwards events whose labels match the principal's.
	// Returning an error closes the connection (typical use: auth
	// rejected). Returning a nil/empty map means "no labels"; events
	// with non-empty labels will be filtered out by DefaultLabelMatcher.
	//
	// When nil, the handler treats every connection as having no labels —
	// suitable for single-tenant deployments where every authenticated
	// user sees every event.
	PrincipalLabels func(r *http.Request) (map[string]string, error)

	// Matcher decides per delivered message whether to forward it to a
	// given connection given its principal's labels. Defaults to
	// DefaultLabelMatcher (Kubernetes-style label selector semantics).
	Matcher LabelMatcher

	// OriginPatterns restricts which Origin headers are accepted on the
	// inbound WS handshake (passed to websocket.AcceptOptions). Empty
	// means "same-origin only" — the secure default. To allow specific
	// browser origins, list them explicitly: ["app.example.com"].
	// To allow ALL origins (cross-site WS hijacking risk), set
	// SkipOriginVerify=true; preferred only behind an upstream proxy
	// that already enforces origin policy.
	OriginPatterns []string

	// SkipOriginVerify disables the WS Origin check entirely. Defaults
	// to false (secure). Only set to true when an upstream layer (e.g.
	// nginx, Cloudflare, IAP) enforces the origin policy upstream.
	SkipOriginVerify bool

	// Logger receives marshaler errors (one bad event dropped, stream
	// stays alive). Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// onSubscribed is a test-only readiness hook fired after every
	// SubscribeBroadcast has registered but before the handler blocks on
	// the outbound select. Lets deterministic tests avoid time.Sleep
	// warm-ups. Intentionally unexported — production callers don't need
	// this and shouldn't depend on it.
	onSubscribed func()
}

// NewBroadcastHandler returns an http.Handler that upgrades the request to
// WebSocket, subscribes to every Subject on the bus via SubscribeBroadcast,
// and forwards each delivered message to the client as a JSON text frame.
//
// The handler exits when the client disconnects, when the request context
// is cancelled, or when any subject subscription fails to register. It is
// safe to mount the same handler at multiple routes (each invocation gets
// its own subscription set).
//
// Auth, rate limiting, tenant filtering: compose with regular HTTP
// middleware on the router. The handler itself is auth-agnostic.
func NewBroadcastHandler(cfg BroadcastConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Bus == nil || cfg.Marshal == nil {
			http.Error(w, "broadcast handler not configured", http.StatusInternalServerError)
			return
		}

		// Resolve the principal's labels BEFORE upgrading — auth failures
		// land on a plain HTTP response code so middleware (Sentry, etc.)
		// can capture them normally.
		var principalLabels map[string]string
		if cfg.PrincipalLabels != nil {
			pl, err := cfg.PrincipalLabels(r)
			if err != nil {
				// Never leak the underlying error to the client — it may
				// carry backend reasons or identifiers. Log via cfg.Logger
				// (defaults to slog.Default) so operators still see it.
				logger := cfg.Logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Warn("events: broadcast principal labels resolution failed",
					"remote", r.RemoteAddr, "err", err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			principalLabels = pl
		}
		matcher := cfg.Matcher
		if matcher == nil {
			matcher = DefaultLabelMatcher
		}

		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns:     cfg.OriginPatterns,
			InsecureSkipVerify: cfg.SkipOriginVerify,
		})
		if err != nil {
			return // websocket library already wrote the response
		}
		defer ws.CloseNow() //nolint:errcheck

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Outbound queue: serialise writes from N subscriber goroutines onto
		// the single WS connection. Buffered to absorb small bursts; a
		// slow client that fills the buffer just gets the connection torn
		// down (which is the right behaviour for a fan-out broadcast).
		out := make(chan []byte, 64)

		var subs []Subscription
		defer func() {
			for _, s := range subs {
				_ = s.Unsubscribe()
			}
		}()

		for _, subj := range cfg.Subjects {
			subj := subj
			sub, err := cfg.Bus.SubscribeBroadcast(subj, func(_ context.Context, m Message) error {
				// Server-side auth filter: drop events whose labels don't
				// match the principal's. This is the security boundary;
				// the client-side envelope-label filter on top of it is
				// pure UX (which screen is the user looking at).
				if !matcher(principalLabels, HeadersToLabels(m.Headers)) {
					return nil
				}
				envelope, mErr := cfg.Marshal(subj, m.Payload, m.Headers)
				if mErr != nil {
					// Malformed event on the bus — drop, don't kill the
					// client. Log so operators see persistent issues.
					logger := cfg.Logger
					if logger == nil {
						logger = slog.Default()
					}
					logger.Warn("events: broadcast marshal failed",
						"subject", subj, "err", mErr)
					return nil
				}
				select {
				case out <- envelope:
				case <-ctx.Done():
				default:
					// Client is too slow / queue full — sever the connection
					// rather than back-pressure the bus.
					cancel()
				}
				return nil
			})
			if err != nil {
				_ = ws.Close(websocket.StatusInternalError, fmt.Sprintf("subscribe %s: %v", subj, err))
				return
			}
			subs = append(subs, sub)
		}
		if cfg.onSubscribed != nil {
			cfg.onSubscribed()
		}

		// Reader goroutine: detect client disconnect / ping. mcp-go has its
		// own loop; here we just need to know when the peer goes away.
		go func() {
			for {
				_, _, err := ws.Read(ctx)
				if err != nil {
					cancel()
					return
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-out:
				if err := ws.Write(ctx, websocket.MessageText, payload); err != nil {
					return
				}
			}
		}
	})
}

// JSONEnvelope is the canonical wire shape emitted to broadcast WS
// clients. The events plugin's generated marshaler produces this struct
// per delivered message; exposed here so handwritten clients (or custom
// marshalers) can match the contract.
//
// Labels piggyback on every envelope so browser clients can apply
// per-screen UX filtering on top of the server-side auth filter
// (e.g. "show only events for the project the user is currently
// viewing"). omitempty keeps the wire compatible with consumers that
// were written against the pre-labels envelope shape.
type JSONEnvelope struct {
	Subject string            `json:"subject"`
	Labels  map[string]string `json:"labels,omitempty"`
	Event   json.RawMessage   `json:"event"`
}

// MarshalJSONEnvelope wraps an already-encoded event payload in the
// canonical envelope. Labels are attached when non-empty (the generated
// marshaler passes the message's HeadersToLabels result here).
func MarshalJSONEnvelope(subject string, event json.RawMessage, labels map[string]string) ([]byte, error) {
	return json.Marshal(JSONEnvelope{Subject: subject, Labels: labels, Event: event})
}

