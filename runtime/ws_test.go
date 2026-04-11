package runtime_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

// mockStreamProxy implements runtime.StreamProxy.
type mockStreamProxy struct {
	recvMessages []proto.Message
	recvIdx      int
	sentMessages []proto.Message
	closeSent    bool
	recvErr      error
}

func (m *mockStreamProxy) Send(msg proto.Message) error {
	m.sentMessages = append(m.sentMessages, msg)
	return nil
}

func (m *mockStreamProxy) Recv() (proto.Message, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	if m.recvIdx >= len(m.recvMessages) {
		return nil, io.EOF
	}
	msg := m.recvMessages[m.recvIdx]
	m.recvIdx++
	return msg, nil
}

func (m *mockStreamProxy) NewRequestMessage() proto.Message {
	return &pb.SimpleRequest{}
}

func (m *mockStreamProxy) CloseSend() error {
	m.closeSent = true
	return nil
}

// mockStreamFactory implements runtime.StreamFactory.
type mockStreamFactory struct {
	proxy runtime.StreamProxy
	err   error
}

func (m *mockStreamFactory) OpenStream(ctx context.Context, conn *grpc.ClientConn) (runtime.StreamProxy, error) {
	return m.proxy, m.err
}

func TestWSHandler_AuthFailure(t *testing.T) {
	factory := &mockStreamFactory{
		proxy: &mockStreamProxy{},
	}

	failAuth := func(ctx context.Context, r *http.Request) ([]byte, error) {
		return nil, fmt.Errorf("unauthorized")
	}

	handler := runtime.WSHandler(nil, factory, failAuth, false)

	// Use a real server for WebSocket tests.
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Try to connect - should get 401 before upgrade.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWSHandler_StreamOpenError(t *testing.T) {
	factory := &mockStreamFactory{
		err: fmt.Errorf("cannot open stream"),
	}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// The WS upgrade succeeds, but the server immediately closes with an error.
	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		// Connection might be rejected, which is also fine.
		return
	}
	defer ws.CloseNow()

	// Reading should fail because the server closed the connection.
	_, _, err = ws.Read(ctx)
	if err == nil {
		t.Fatal("expected error when reading from closed connection")
	}
}

func TestWSHandler_ReceiveMessages(t *testing.T) {
	proxy := &mockStreamProxy{
		recvMessages: []proto.Message{
			&pb.SimpleResponse{Id: "msg1", Name: "hello"},
		},
	}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.CloseNow()

	// Read the message sent from gRPC stream -> WS.
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var resp pb.SimpleResponse
	if err := protojson.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Id != "msg1" {
		t.Fatalf("expected id 'msg1', got %q", resp.Id)
	}
}

func TestWSHandler_SendMessage(t *testing.T) {
	// Create a proxy that blocks on Recv (simulating a long-lived stream).
	block := make(chan struct{})
	proxy := &mockStreamProxy{
		recvErr: nil,
	}
	// Override Recv to block.
	blockingProxy := &blockingStreamProxy{
		mockStreamProxy: proxy,
		block:           block,
	}
	factory := &mockStreamFactory{proxy: blockingProxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Send a message from WS -> gRPC.
	req := &pb.SimpleRequest{Name: "test-send"}
	data, _ := protojson.Marshal(req)
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Give time for the message to be processed.
	time.Sleep(100 * time.Millisecond)

	// Close WS to end the handler.
	ws.Close(websocket.StatusNormalClosure, "done")
	close(block)

	// Verify message was sent to stream.
	time.Sleep(100 * time.Millisecond)
	if len(blockingProxy.sentMessages) == 0 {
		t.Fatal("expected at least one sent message")
	}
}

// blockingStreamProxy wraps mockStreamProxy but blocks Recv until unblocked.
type blockingStreamProxy struct {
	*mockStreamProxy
	block chan struct{}
}

func (b *blockingStreamProxy) Recv() (proto.Message, error) {
	<-b.block
	return nil, io.EOF
}

func (b *blockingStreamProxy) Send(msg proto.Message) error {
	return b.mockStreamProxy.Send(msg)
}

func (b *blockingStreamProxy) NewRequestMessage() proto.Message {
	return b.mockStreamProxy.NewRequestMessage()
}

func (b *blockingStreamProxy) CloseSend() error {
	return b.mockStreamProxy.CloseSend()
}

func TestWSHandler_SendInvalidJSON(t *testing.T) {
	block := make(chan struct{})
	proxy := &mockStreamProxy{}
	blockingProxy := &blockingStreamProxy{
		mockStreamProxy: proxy,
		block:           block,
	}
	factory := &mockStreamFactory{proxy: blockingProxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.CloseNow()

	// Send invalid JSON.
	if err := ws.Write(ctx, websocket.MessageText, []byte("{invalid json")); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// The server should close the connection with StatusInvalidFramePayloadData.
	_, _, err = ws.Read(ctx)
	if err == nil {
		t.Fatal("expected error after sending invalid JSON")
	}

	close(block)
}

func TestWSHandler_StreamRecvError(t *testing.T) {
	proxy := &mockStreamProxy{
		recvErr: fmt.Errorf("recv failed"),
	}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.CloseNow()

	// The server should close the connection because stream recv returns an error.
	_, _, err = ws.Read(ctx)
	if err == nil {
		t.Fatal("expected error when stream recv fails")
	}
}

// sendErrorStreamProxy blocks on Recv but returns error on Send.
type sendErrorStreamProxy struct {
	*mockStreamProxy
	block chan struct{}
}

func (s *sendErrorStreamProxy) Recv() (proto.Message, error) {
	<-s.block
	return nil, io.EOF
}

func (s *sendErrorStreamProxy) Send(msg proto.Message) error {
	return fmt.Errorf("send failed")
}

func (s *sendErrorStreamProxy) NewRequestMessage() proto.Message {
	return s.mockStreamProxy.NewRequestMessage()
}

func (s *sendErrorStreamProxy) CloseSend() error {
	return s.mockStreamProxy.CloseSend()
}

func TestWSHandler_NonWebSocketRequest(t *testing.T) {
	proxy := &mockStreamProxy{}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Send a normal HTTP request (not a WebSocket upgrade).
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	// websocket.Accept should fail and write an error response.
	// The exact status depends on the library implementation.
}

func TestWSHandler_AuthSuccess(t *testing.T) {
	proxy := &mockStreamProxy{
		recvMessages: []proto.Message{
			&pb.SimpleResponse{Id: "authed"},
		},
	}
	factory := &mockStreamFactory{proxy: proxy}

	successAuth := func(ctx context.Context, r *http.Request) ([]byte, error) {
		return []byte("user-data"), nil
	}

	handler := runtime.WSHandler(nil, factory, successAuth, false)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.CloseNow()

	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var resp pb.SimpleResponse
	if err := protojson.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Id != "authed" {
		t.Fatalf("expected id 'authed', got %q", resp.Id)
	}
}

// delayedStreamProxy delivers messages with a delay to ensure writes happen after disconnect.
type delayedStreamProxy struct {
	messages []proto.Message
	idx      int
	delay    time.Duration
}

func (d *delayedStreamProxy) Recv() (proto.Message, error) {
	if d.idx >= len(d.messages) {
		// Block forever (simulate long-lived stream).
		select {}
	}
	if d.idx > 0 {
		time.Sleep(d.delay)
	}
	msg := d.messages[d.idx]
	d.idx++
	return msg, nil
}
func (d *delayedStreamProxy) Send(msg proto.Message) error         { return nil }
func (d *delayedStreamProxy) NewRequestMessage() proto.Message     { return &pb.SimpleRequest{} }
func (d *delayedStreamProxy) CloseSend() error                     { return nil }

func TestWSHandler_WriteErrorOnClientDisconnect(t *testing.T) {
	proxy := &delayedStreamProxy{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
			&pb.SimpleResponse{Id: "2"},
		},
		delay: 100 * time.Millisecond,
	}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Read the first message.
	_, _, err = ws.Read(ctx)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	// Close immediately -- the delayed second message write should fail.
	ws.CloseNow()
	time.Sleep(300 * time.Millisecond)
}

func TestWSHandler_StreamSendError(t *testing.T) {
	block := make(chan struct{})
	proxy := &sendErrorStreamProxy{
		mockStreamProxy: &mockStreamProxy{},
		block:           block,
	}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer ws.CloseNow()

	// Send a valid message -- the server's stream.Send will fail.
	req := &pb.SimpleRequest{Name: "test"}
	data, _ := protojson.Marshal(req)
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Give time for server to process and close.
	time.Sleep(100 * time.Millisecond)
	close(block)
}

func TestWSHandler_GoroutineCleanupOnDisconnect(t *testing.T) {
	// Regression: verify that when a WS client disconnects, the recv goroutine
	// exits and does not leak.
	block := make(chan struct{})
	proxy := &blockingStreamProxy{
		mockStreamProxy: &mockStreamProxy{},
		block:           block,
	}
	factory := &mockStreamFactory{proxy: proxy}

	handler := runtime.WSHandler(nil, factory, runtime.NoAuth(), true)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Snapshot goroutine count before opening the WS connection.
	goroutinesBefore := goruntime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Close WS client immediately to trigger disconnect.
	ws.CloseNow()

	// Unblock the recv goroutine so it can detect the context cancellation.
	close(block)

	// Give goroutines time to clean up.
	time.Sleep(300 * time.Millisecond)

	goroutinesAfter := goruntime.NumGoroutine()

	// Allow a tolerance of 5 goroutines for background runtime activity.
	if goroutinesAfter > goroutinesBefore+5 {
		t.Fatalf("possible goroutine leak: before=%d, after=%d", goroutinesBefore, goroutinesAfter)
	}
}
