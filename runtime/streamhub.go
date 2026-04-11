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

// subscriber wraps a message channel with safe-close semantics.
type subscriber struct {
	ch   chan proto.Message
	once sync.Once
}

// closeCh safely closes the message channel exactly once.
func (s *subscriber) closeCh() {
	s.once.Do(func() {
		close(s.ch)
	})
}

type userStream struct {
	mu          sync.Mutex
	subscribers map[int64]*subscriber
	nextID      int64
	cancel      context.CancelFunc
	starting    bool // true while opener() is in progress, prevents duplicate opens
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
			subscribers: make(map[int64]*subscriber),
		}
		h.streams[userID] = us
	}

	// Register subscriber
	us.mu.Lock()
	id := us.nextID
	us.nextID++
	sub := &subscriber{ch: make(chan proto.Message, 32)}
	us.subscribers[id] = sub
	subscriberCount := len(us.subscribers)

	// Determine if we need to start the stream. Use the starting flag
	// to prevent a race where multiple goroutines release h.mu and all
	// call opener() concurrently.
	needStart := false
	if (!exists || subscriberCount == 1) && !us.starting {
		us.starting = true
		needStart = true
	}
	us.mu.Unlock()

	h.mu.Unlock()

	// If this is the first subscriber, start the gRPC stream
	if needStart {
		streamCtx, cancel := context.WithCancel(ctx)
		us.mu.Lock()
		us.cancel = cancel
		us.mu.Unlock()

		stream, err := opener(streamCtx, conn)
		if err != nil {
			// Cleanup on failure
			us.mu.Lock()
			us.starting = false
			us.mu.Unlock()
			h.unsubscribe(userID, id)
			cancel()
			return nil, nil, err
		}

		go h.receiveLoop(userID, us, stream)
	}

	unsub := func() {
		h.unsubscribe(userID, id)
	}

	return sub.ch, unsub, nil
}

func (h *UserStreamHub) receiveLoop(userID string, us *userStream, stream ServerStream) {
	defer recoverGoroutine()
	defer func() {
		h.mu.Lock()
		// Only clean up if this is still the active stream for the user
		if current, ok := h.streams[userID]; ok && current == us {
			delete(h.streams, userID)
		}
		h.mu.Unlock()

		us.mu.Lock()
		for _, sub := range us.subscribers {
			sub.closeCh()
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
			if err != io.EOF && !isClientGone(err) {
				// Unexpected stream error → log (not Sentry, these are often transient).
				logError(err)
			}
			return
		}

		us.mu.Lock()
		for id, sub := range us.subscribers {
			select {
			case sub.ch <- msg:
			default:
				// Slow subscriber: close its channel to force reconnect
				// instead of silently dropping messages.
				sub.closeCh()
				delete(us.subscribers, id)
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
	if sub, ok := us.subscribers[subID]; ok {
		sub.closeCh()
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
