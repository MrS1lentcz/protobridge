package runtime_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

// mockServerStreamOpener implements runtime.ServerStreamOpener.
type mockServerStreamOpener struct {
	stream runtime.ServerStream
	err    error
}

func (m *mockServerStreamOpener) OpenServerStream(ctx context.Context, conn *grpc.ClientConn, req proto.Message) (runtime.ServerStream, error) {
	return m.stream, m.err
}

func (m *mockServerStreamOpener) NewRequestMessage() proto.Message {
	return &pb.SimpleRequest{}
}

func TestSSEHandler_StreamsMessages(t *testing.T) {
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1", Name: "first"},
			&pb.SimpleResponse{Id: "2", Name: "second"},
		},
	}

	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("expected SSE data frames, got: %s", body)
	}
	if !strings.Contains(body, `"id":"1"`) {
		t.Fatalf("expected message with id=1, got: %s", body)
	}
	if !strings.Contains(body, `"id":"2"`) {
		t.Fatalf("expected message with id=2, got: %s", body)
	}
	// After EOF, should have close event.
	if !strings.Contains(body, "event: close") {
		t.Fatalf("expected close event after stream end, got: %s", body)
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}
}

func TestSSEHandler_OpenStreamError(t *testing.T) {
	opener := &mockServerStreamOpener{
		err: fmt.Errorf("stream open failed"),
	}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	// Should write a gRPC error response (non-gRPC error = 500).
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestSSEHandler_AuthFailure(t *testing.T) {
	opener := &mockServerStreamOpener{
		stream: &mockServerStream{},
	}

	failAuth := func(ctx context.Context, r *http.Request) ([]byte, error) {
		return nil, fmt.Errorf("unauthorized")
	}

	handler := runtime.SSEHandler(nil, opener, failAuth, false)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSSEHandler_StreamError(t *testing.T) {
	stream := &mockServerStream{
		err: fmt.Errorf("stream broke"),
	}

	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected error event, got: %s", body)
	}
}

// errorRecvStream returns an error after first message.
type errorAfterOneStream struct {
	first bool
}

func (e *errorAfterOneStream) Recv() (proto.Message, error) {
	if !e.first {
		e.first = true
		return &pb.SimpleResponse{Id: "ok"}, nil
	}
	return nil, io.EOF
}

func TestSSEHandler_SingleMessageThenEOF(t *testing.T) {
	stream := &errorAfterOneStream{}
	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `"id":"ok"`) {
		t.Fatalf("expected message, got: %s", body)
	}
	if !strings.Contains(body, "event: close") {
		t.Fatalf("expected close event, got: %s", body)
	}
}

// ctxCancelledStream delivers one message, waits for context to be cancelled,
// then delivers another message (which will trigger the ctx.Err() check).
type ctxCancelledStream struct {
	count    int
	cancelFn context.CancelFunc
}

func (s *ctxCancelledStream) Recv() (proto.Message, error) {
	s.count++
	if s.count == 1 {
		// First message succeeds normally.
		return &pb.SimpleResponse{Id: "msg1"}, nil
	}
	// Cancel the context before returning the second message.
	// This ensures ctx.Err() != nil when checked after the next write.
	s.cancelFn()
	return &pb.SimpleResponse{Id: "msg2"}, nil
}

func TestSSEHandler_ClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &ctxCancelledStream{cancelFn: cancel}

	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `"id":"msg1"`) {
		t.Fatalf("expected first message, got: %s", body)
	}
}

// nonFlusherWriter is an http.ResponseWriter that does NOT implement http.Flusher.
type nonFlusherWriter struct {
	header http.Header
	body   strings.Builder
	code   int
}

func (w *nonFlusherWriter) Header() http.Header        { return w.header }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *nonFlusherWriter) WriteHeader(code int)        { w.code = code }

func TestSSEHandler_NonFlusherWriter(t *testing.T) {
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
		},
	}

	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := &nonFlusherWriter{header: make(http.Header)}

	handler.ServeHTTP(w, r)

	// The handler should detect no Flusher and write an error.
	if !strings.Contains(w.body.String(), "streaming not supported") {
		t.Fatalf("expected 'streaming not supported' error, got: %s", w.body.String())
	}
}

func TestSSEHandler_WriterDisconnect(t *testing.T) {
	// Regression: when the request context is cancelled mid-stream (client
	// disconnects), the handler should return promptly and not hang.
	ctx, cancel := context.WithCancel(context.Background())

	// Stream that delivers messages slowly and cancels context after 2nd msg.
	msgCount := 0
	stream := &slowCancelStream{
		cancel: cancel,
		count:  &msgCount,
	}

	opener := &mockServerStreamOpener{stream: stream}
	handler := runtime.SSEHandler(nil, opener, runtime.NoAuth(), true)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, r)
		close(done)
	}()

	select {
	case <-done:
		// Handler returned -- not hanging. Good.
	case <-time.After(3 * time.Second):
		t.Fatal("SSE handler hung after context cancellation")
	}
}

// slowCancelStream delivers one message, then cancels the context, then returns another.
type slowCancelStream struct {
	cancel context.CancelFunc
	count  *int
}

func (s *slowCancelStream) Recv() (proto.Message, error) {
	*s.count++
	if *s.count == 1 {
		return &pb.SimpleResponse{Id: "msg1"}, nil
	}
	// Cancel context before returning second message.
	s.cancel()
	// Simulate a slight delay.
	time.Sleep(10 * time.Millisecond)
	return &pb.SimpleResponse{Id: "msg2"}, nil
}

func TestSSEHandler_AuthSuccess(t *testing.T) {
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
		},
	}

	opener := &mockServerStreamOpener{stream: stream}
	successAuth := func(ctx context.Context, r *http.Request) ([]byte, error) {
		return []byte("user-data"), nil
	}

	handler := runtime.SSEHandler(nil, opener, successAuth, false)

	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `"id":"1"`) {
		t.Fatalf("expected message, got: %s", body)
	}
}
