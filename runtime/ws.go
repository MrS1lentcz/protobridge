package runtime

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// WSAcceptConfig is threaded from the generated WS handlers into
// WSAcceptOptions. Per-RPC patterns come from (protobridge.ws_origin_patterns).
type WSAcceptConfig struct {
	// PerRPCPatterns is the comma-separated Origin allow-list declared on
	// the individual RPC. Merged union-style with the env-wide list. Empty
	// means "use the env list only".
	PerRPCPatterns string
}

// ErrWSInsecureInProduction is the message panicked by InitWSConfig and
// WSAcceptOptions when the dev-only origin-skip escape hatch is enabled
// under a production marker. Exposed as a named constant so deployment
// automation can grep for it in crash logs.
const ErrWSInsecureInProduction = "runtime: PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY=true is forbidden when PROTOBRIDGE_ENV=production"

// InitWSConfig validates WS-related environment variables at process
// startup. The generated gateway main invokes it before any router is
// registered, so misconfiguration fails fast — before traffic hits the
// first WS upgrade and before downstream connections are established.
//
// Currently it refuses the combination
// PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY=true + PROTOBRIDGE_ENV=production
// (panics with ErrWSInsecureInProduction). Non-production processes are
// allowed to toggle skip-verify for local development.
//
// Safe to call more than once; idempotent. Callers outside the generated
// gateway (e.g. custom bootstraps) should invoke it explicitly once
// after parsing env.
func InitWSConfig() {
	if os.Getenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY") != "true" {
		return
	}
	if strings.EqualFold(os.Getenv("PROTOBRIDGE_ENV"), "production") {
		panic(ErrWSInsecureInProduction)
	}
}

// WSAcceptOptions resolves the coder/websocket.AcceptOptions for a single
// WS upgrade. It merges WSAcceptConfig.PerRPCPatterns with the env-wide
// PROTOBRIDGE_WS_ORIGIN_PATTERNS allow-list, and honours the dev-only
// PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY escape hatch.
//
// Misconfiguration (skip-verify in production) normally trips InitWSConfig
// at startup; the same check is mirrored here as defence in depth for
// bootstraps that skip InitWSConfig — panics with ErrWSInsecureInProduction
// rather than log.Fatal so deferred server-shutdown runs and the panic
// surfaces through tests.
//
// Returns nil when neither per-RPC nor env patterns are set and skip-verify
// is off; coder/websocket then falls back to the default same-origin check.
// Env vars are read at call time so tests can toggle them per-case.
func WSAcceptOptions(cfg WSAcceptConfig) *websocket.AcceptOptions {
	if os.Getenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY") == "true" {
		if strings.EqualFold(os.Getenv("PROTOBRIDGE_ENV"), "production") {
			panic(ErrWSInsecureInProduction)
		}
		return &websocket.AcceptOptions{InsecureSkipVerify: true}
	}

	patterns := mergeOriginPatterns(cfg.PerRPCPatterns, os.Getenv("PROTOBRIDGE_WS_ORIGIN_PATTERNS"))
	if len(patterns) == 0 {
		return nil
	}
	return &websocket.AcceptOptions{OriginPatterns: patterns}
}

// mergeOriginPatterns unions two comma-separated lists, preserving the
// first-seen order and dropping duplicates / empty entries. Trimming is
// applied so users can write "a.com, b.com" without the space ending up
// in the allow-list key.
func mergeOriginPatterns(perRPC, global string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, src := range []string{perRPC, global} {
		if src == "" {
			continue
		}
		for _, raw := range strings.Split(src, ",") {
			p := strings.TrimSpace(raw)
			if p == "" {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// UnmarshalWSFrame decodes a single WebSocket frame into a typed proto.
// Text frames are JSON (protojson) — same envelope the gRPC→WS direction
// already writes — while binary frames are wire-format proto, letting
// non-browser clients skip the JSON encode/decode round-trip. The caller
// passes the msgType returned by websocket.Conn.Read so this helper can
// dispatch without another opcode peek.
func UnmarshalWSFrame(msgType websocket.MessageType, data []byte, m proto.Message) error {
	if msgType == websocket.MessageBinary {
		return proto.Unmarshal(data, m)
	}
	return protojson.Unmarshal(data, m)
}

// StreamFactory creates a gRPC stream from a client connection and context.
// The generated code provides concrete implementations per streaming RPC.
type StreamFactory interface {
	// OpenStream opens the gRPC stream and returns a bidirectional message channel.
	OpenStream(ctx context.Context, conn *grpc.ClientConn) (StreamProxy, error)
}

// StreamProxy abstracts a gRPC stream for WebSocket proxying.
type StreamProxy interface {
	// Send sends a message to the gRPC stream. Nil msg means close send.
	Send(msg proto.Message) error
	// Recv receives a message from the gRPC stream.
	Recv() (proto.Message, error)
	// NewRequestMessage returns a new empty instance of the request message type.
	NewRequestMessage() proto.Message
	// CloseSend signals that no more messages will be sent.
	CloseSend() error
}

// WSHandler creates an HTTP handler that upgrades to WebSocket and proxies
// messages to/from a gRPC stream.
func WSHandler(conn *grpc.ClientConn, factory StreamFactory, auth AuthFunc, excludeAuth bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Auth
		if !excludeAuth {
			userData, err := auth(ctx, r)
			if err != nil {
				WriteAuthError(w, err)
				return
			}
			ctx = SetUserMetadata(ctx, userData)
		}

		// Upgrade — same PROTOBRIDGE_WS_ORIGIN_PATTERNS semantics as
		// generated handlers, so users wiring WSHandler directly get the
		// env-wide allow-list without extra plumbing.
		ws, err := websocket.Accept(w, r, WSAcceptOptions(WSAcceptConfig{}))
		if err != nil {
			return // Accept already wrote the error response
		}
		defer func() { _ = ws.CloseNow() }()

		// Open gRPC stream
		stream, err := factory.OpenStream(ctx, conn)
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "failed to open stream")
			// Only report non-transient errors to Sentry.
			if !isClientGone(err) {
				reportError(err)
			}
			return
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// gRPC → WebSocket
		go func() {
			defer recoverGoroutine()
			defer cancel()
			for {
				if ctx.Err() != nil {
					return
				}
				msg, err := stream.Recv()
				if err != nil {
					if err == io.EOF || ctx.Err() != nil {
						_ = ws.Close(websocket.StatusNormalClosure, "stream ended")
						return
					}
					// Stream error is logged (not Sentry) unless it's a server bug.
					logError(err)
					_ = ws.Close(websocket.StatusInternalError, "stream error")
					return
				}
				data, err := protojson.Marshal(msg)
				if err != nil {
					// Marshal error on valid proto = bug → Sentry.
					reportError(err)
					_ = ws.Close(websocket.StatusInternalError, "marshal error")
					return
				}
				if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
					// Client disconnected – normal, no logging needed.
					return
				}
			}
		}()

		// WebSocket → gRPC
		for {
			msgType, data, err := ws.Read(ctx)
			if err != nil {
				// Client disconnected or context cancelled – normal.
				_ = stream.CloseSend()
				return
			}
			msg := stream.NewRequestMessage()
			if err := UnmarshalWSFrame(msgType, data, msg); err != nil {
				_ = ws.Close(websocket.StatusInvalidFramePayloadData, "invalid frame payload")
				return
			}
			if err := stream.Send(msg); err != nil {
				return
			}
		}
	}
}
