package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ServerStreamOpener opens a server-streaming gRPC call and returns
// a receive-only stream. Generated code provides concrete implementations.
type ServerStreamOpener interface {
	OpenServerStream(ctx context.Context, conn *grpc.ClientConn, req proto.Message) (ServerStream, error)
	NewRequestMessage() proto.Message
}

// ServerStream abstracts a server-streaming gRPC response.
type ServerStream interface {
	Recv() (proto.Message, error)
}

// SSEHandler creates an HTTP handler that streams server-sent events from
// a gRPC server stream. Each message becomes one SSE "data:" frame.
func SSEHandler(conn *grpc.ClientConn, opener ServerStreamOpener, auth AuthFunc, excludeAuth bool) http.HandlerFunc {
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

		// Open gRPC server stream
		req := opener.NewRequestMessage()
		stream, err := opener.OpenServerStream(ctx, conn, req)
		if err != nil {
			WriteGRPCError(w, err)
			return
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", "streaming not supported")
			return
		}

		// Stream events
		for {
			msg, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Stream ended normally – not an error.
					if _, err := fmt.Fprintf(w, "event: close\ndata: {}\n\n"); err != nil {
						return
					}
					flusher.Flush()
					return
				}
				if isClientGone(err) {
					// Client disconnected – normal lifecycle, no logging.
					return
				}
				// Unexpected stream error – log to stderr (not Sentry,
				// these are often transient backend issues).
				logError(err)
				if _, err := fmt.Fprintf(w, "event: error\ndata: {\"message\":%q}\n\n", err.Error()); err != nil {
					return
				}
				flusher.Flush()
				return
			}

			data, err := protojson.Marshal(msg)
			if err != nil {
				// Marshal error on valid proto3 = bug → Sentry.
				reportError(err)
				continue
			}

			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				// Client disconnected.
				return
			}
			flusher.Flush()

			// Check if client disconnected.
			if ctx.Err() != nil {
				return
			}
		}
	}
}
