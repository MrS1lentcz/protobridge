package runtime

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// UserStreamHub multiplexes a single gRPC server stream across multiple
// WebSocket/SSE connections for the same user. When user "alice" opens
// 3 browser tabs, only 1 gRPC stream is created – all 3 tabs receive
// the same messages.
//
// Only used for server-streaming + private ws_mode. Bidi and client
// streaming always use 1:1 mapping.
type UserStreamHub struct {
	mu      sync.Mutex
	streams map[string]*userStream // user_id → active stream
}

type userStream struct {
	mu          sync.Mutex
	subscribers map[int64]chan proto.Message
	nextID      int64
	cancel      context.CancelFunc
}

// NewUserStreamHub creates a new hub.
func NewUserStreamHub() *UserStreamHub {
	return &UserStreamHub{
		streams: make(map[string]*userStream),
	}
}

// Subscribe registers a new subscriber for the given user. If no gRPC stream
// exists for this user yet, one is created via the opener function. Returns
// a channel that receives messages and an unsubscribe function.
//
// The opener function should open a gRPC server stream for the given context
// and return a receive-only interface.
func (h *UserStreamHub) Subscribe(
	ctx context.Context,
	userID string,
	conn *grpc.ClientConn,
	opener func(ctx context.Context, conn *grpc.ClientConn) (ServerStream, error),
) (<-chan proto.Message, func(), error) {
	h.mu.Lock()

	us, exists := h.streams[userID]
	if !exists {
		us = &userStream{
			subscribers: make(map[int64]chan proto.Message),
		}
		h.streams[userID] = us
	}

	// Register subscriber
	us.mu.Lock()
	id := us.nextID
	us.nextID++
	ch := make(chan proto.Message, 32)
	us.subscribers[id] = ch
	subscriberCount := len(us.subscribers)
	us.mu.Unlock()

	h.mu.Unlock()

	// If this is the first subscriber, start the gRPC stream
	if !exists || subscriberCount == 1 {
		streamCtx, cancel := context.WithCancel(ctx)
		us.mu.Lock()
		us.cancel = cancel
		us.mu.Unlock()

		stream, err := opener(streamCtx, conn)
		if err != nil {
			// Cleanup on failure
			h.unsubscribe(userID, id)
			cancel()
			return nil, nil, err
		}

		go h.receiveLoop(userID, us, stream)
	}

	unsub := func() {
		h.unsubscribe(userID, id)
	}

	return ch, unsub, nil
}

func (h *UserStreamHub) receiveLoop(userID string, us *userStream, stream ServerStream) {
	defer func() {
		h.mu.Lock()
		// Only clean up if this is still the active stream for the user
		if current, ok := h.streams[userID]; ok && current == us {
			delete(h.streams, userID)
		}
		h.mu.Unlock()

		us.mu.Lock()
		for _, ch := range us.subscribers {
			close(ch)
		}
		us.subscribers = nil
		if us.cancel != nil {
			us.cancel()
		}
		us.mu.Unlock()
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				reportError(err)
			}
			return
		}

		us.mu.Lock()
		for _, ch := range us.subscribers {
			select {
			case ch <- msg:
			default:
				// Slow subscriber, drop message
			}
		}
		us.mu.Unlock()
	}
}

func (h *UserStreamHub) unsubscribe(userID string, subID int64) {
	h.mu.Lock()
	us, exists := h.streams[userID]
	if !exists {
		h.mu.Unlock()
		return
	}

	us.mu.Lock()
	if ch, ok := us.subscribers[subID]; ok {
		close(ch)
		delete(us.subscribers, subID)
	}
	remaining := len(us.subscribers)
	us.mu.Unlock()

	// Last subscriber gone → stop the gRPC stream
	if remaining == 0 {
		delete(h.streams, userID)
		h.mu.Unlock()
		us.mu.Lock()
		if us.cancel != nil {
			us.cancel()
		}
		us.mu.Unlock()
	} else {
		h.mu.Unlock()
	}
}
