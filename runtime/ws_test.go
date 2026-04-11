package runtime_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	if len(blockingProxy.mockStreamProxy.sentMessages) == 0 {
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
