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
type JSONEnvelope struct {
	Subject string          `json:"subject"`
	Event   json.RawMessage `json:"event"`
}

// MarshalJSONEnvelope is a small helper for the generated marshaler to
// avoid reimplementing the wrapping. event is already-encoded JSON
// (typically protojson output of the typed message).
func MarshalJSONEnvelope(subject string, event json.RawMessage) ([]byte, error) {
	return json.Marshal(JSONEnvelope{Subject: subject, Event: event})
}

