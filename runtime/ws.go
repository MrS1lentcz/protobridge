package runtime

import (
	"context"
	"io"
	"net/http"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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

		// Upgrade
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept already wrote the error response
		}
		defer ws.CloseNow()

		// Open gRPC stream
		stream, err := factory.OpenStream(ctx, conn)
		if err != nil {
			ws.Close(websocket.StatusInternalError, "failed to open stream")
			reportError(err)
			return
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// gRPC → WebSocket
		go func() {
			defer cancel()
			for {
				msg, err := stream.Recv()
				if err != nil {
					if err == io.EOF {
						ws.Close(websocket.StatusNormalClosure, "stream ended")
						return
					}
					ws.Close(websocket.StatusInternalError, "stream error")
					return
				}
				data, err := protojson.Marshal(msg)
				if err != nil {
					reportError(err)
					ws.Close(websocket.StatusInternalError, "marshal error")
					return
				}
				if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
					return
				}
			}
		}()

		// WebSocket → gRPC
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				// Client disconnected or context cancelled
				stream.CloseSend()
				return
			}
			msg := stream.NewRequestMessage()
			if err := protojson.Unmarshal(data, msg); err != nil {
				ws.Close(websocket.StatusInvalidFramePayloadData, "invalid JSON")
				return
			}
			if err := stream.Send(msg); err != nil {
				return
			}
		}
	}
}
