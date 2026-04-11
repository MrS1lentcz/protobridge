package runtime_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

// mockServerStream implements runtime.ServerStream for testing.
type mockServerStream struct {
	mu       sync.Mutex
	messages []proto.Message
	idx      int
	err      error
	block    chan struct{} // if non-nil, Recv blocks until closed
}

func (m *mockServerStream) Recv() (proto.Message, error) {
	if m.block != nil {
		<-m.block
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if m.idx >= len(m.messages) {
		return nil, io.EOF
	}
	msg := m.messages[m.idx]
	m.idx++
	return msg, nil
}

func TestUserStreamHub_SingleSubscriber(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1", Name: "msg1"},
			&pb.SimpleResponse{Id: "2", Name: "msg2"},
		},
	}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	ch, unsub, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer unsub()

	// Read messages from channel.
	var received []string
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case msg, ok := <-ch:
			if !ok {
				break
			}
			resp, _ := msg.(*pb.SimpleResponse)
			received = append(received, resp.GetId())
		case <-timeout:
			t.Fatal("timed out waiting for messages")
		}
	}

	if len(received) != 2 || received[0] != "1" || received[1] != "2" {
		t.Fatalf("expected [1, 2], got %v", received)
	}

	// Channel should be closed after EOF.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestUserStreamHub_OpenerError(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return nil, fmt.Errorf("connection failed")
	}

	_, _, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err == nil {
		t.Fatal("expected error from failing opener")
	}
	if err.Error() != "connection failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUserStreamHub_MultipleSubscribers(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	block := make(chan struct{})
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "shared"},
		},
		block: block,
	}

	openerCalls := 0
	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		openerCalls++
		return stream, nil
	}

	ch1, unsub1, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer unsub1()

	ch2, unsub2, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer unsub2()

	// Only one stream should be opened.
	if openerCalls != 1 {
		t.Fatalf("expected 1 opener call, got %d", openerCalls)
	}

	// Unblock the stream.
	close(block)

	// Both channels should receive the message.
	timeout := time.After(2 * time.Second)
	for i, ch := range []<-chan proto.Message{ch1, ch2} {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("channel %d closed unexpectedly", i)
			}
			resp := msg.(*pb.SimpleResponse)
			if resp.GetId() != "shared" {
				t.Fatalf("channel %d: expected id 'shared', got %q", i, resp.GetId())
			}
		case <-timeout:
			t.Fatalf("channel %d: timed out", i)
		}
	}
}

func TestUserStreamHub_StreamError(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	stream := &mockServerStream{
		err: fmt.Errorf("stream error"),
	}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	ch, unsub, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer unsub()

	// Channel should be closed after stream error.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after stream error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestUserStreamHub_DoubleUnsubscribe(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	block := make(chan struct{})
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
		},
		block: block,
	}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	_, unsub, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unsubscribe twice -- second should be a no-op.
	unsub()
	unsub()

	close(block)
	time.Sleep(50 * time.Millisecond)
}

func TestUserStreamHub_SlowSubscriber(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	// Create a stream with many messages to overflow subscriber buffer.
	msgs := make([]proto.Message, 100)
	for i := range msgs {
		msgs[i] = &pb.SimpleResponse{Id: fmt.Sprintf("%d", i)}
	}
	stream := &mockServerStream{messages: msgs}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	ch, unsub, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer unsub()

	// Don't read from ch -- let it overflow (buffer is 32).
	// Wait for stream to end.
	select {
	case <-ch:
		// Got at least one message or channel closed.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestUserStreamHub_UnsubscribeOneOfMany(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	block := make(chan struct{})
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
		},
		block: block,
	}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	_, unsub1, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, unsub2, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unsubscribe only the first -- stream should stay alive for the second.
	unsub1()

	// Unblock and clean up.
	close(block)
	time.Sleep(50 * time.Millisecond)
	unsub2()
}

func TestUserStreamHub_UnsubscribeLastStopsStream(t *testing.T) {
	hub := runtime.NewUserStreamHub()

	block := make(chan struct{})
	stream := &mockServerStream{
		messages: []proto.Message{
			&pb.SimpleResponse{Id: "1"},
		},
		block: block,
	}

	opener := func(ctx context.Context, conn *grpc.ClientConn) (runtime.ServerStream, error) {
		return stream, nil
	}

	_, unsub, err := hub.Subscribe(context.Background(), "user1", nil, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unsubscribe immediately (before any messages).
	unsub()

	// Unblock stream so it can finish.
	close(block)

	// Give time for cleanup.
	time.Sleep(50 * time.Millisecond)
}
