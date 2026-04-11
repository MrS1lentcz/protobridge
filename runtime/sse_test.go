package runtime_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
