package runtime_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/mrs1lentcz/gox/grpcx"
	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestUnaryCallWithRetry_SuccessFirstTry(t *testing.T) {
	callCount := 0
	call := func(ctx context.Context, req *pb.SimpleRequest) (*pb.SimpleResponse, error) {
		callCount++
		return &pb.SimpleResponse{Id: "ok"}, nil
	}

	resp, err := runtime.UnaryCallWithRetry(context.Background(), nil, "addr", call, &pb.SimpleRequest{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != "ok" {
		t.Fatalf("expected id 'ok', got %q", resp.Id)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestUnaryCallWithRetry_NonTransientError(t *testing.T) {
	callCount := 0
	call := func(ctx context.Context, req *pb.SimpleRequest) (*pb.SimpleResponse, error) {
		callCount++
		return nil, status.Error(codes.InvalidArgument, "bad request")
	}

	_, err := runtime.UnaryCallWithRetry(context.Background(), nil, "addr", call, &pb.SimpleRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call for non-transient error, got %d", callCount)
	}
}

func TestReconnectOnTransient_NilError(t *testing.T) {
	conn, ok := runtime.ReconnectOnTransient(nil, "addr", nil)
	if ok {
		t.Fatal("expected false for nil error")
	}
	if conn != nil {
		t.Fatal("expected nil conn for nil error")
	}
}

func TestReconnectOnTransient_NonTransientError(t *testing.T) {
	err := status.Error(codes.InvalidArgument, "bad")
	conn, ok := runtime.ReconnectOnTransient(nil, "addr", err)
	if ok {
		t.Fatal("expected false for non-transient error")
	}
	if conn != nil {
		t.Fatal("expected nil conn")
	}
}

func TestUnaryCallWithRetry_TransientError_ReconnectFails(t *testing.T) {
	// Use a real pool that has no connection for "addr", so Reconnect will fail.
	pool := grpcx.NewPool()
	defer func() { _ = pool.Close() }()

	callCount := 0
	call := func(ctx context.Context, req *pb.SimpleRequest) (*pb.SimpleResponse, error) {
		callCount++
		return nil, status.Error(codes.Unavailable, "down")
	}

	_, err := runtime.UnaryCallWithRetry(context.Background(), pool, "bad-addr", call, &pb.SimpleRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	// Should have been called once (no retry because reconnect fails).
	if callCount != 1 {
		t.Fatalf("expected 1 call (reconnect failed), got %d", callCount)
	}
}

func TestUnaryCallWithRetry_TransientError_ReconnectSucceeds(t *testing.T) {
	pool := grpcx.NewPool()
	defer func() { _ = pool.Close() }()

	// Pre-connect to an address so Reconnect can succeed.
	addr := "localhost:0"
	_, _ = pool.Connect(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))

	callCount := 0
	call := func(ctx context.Context, req *pb.SimpleRequest) (*pb.SimpleResponse, error) {
		callCount++
		if callCount == 1 {
			return nil, status.Error(codes.Unavailable, "transient")
		}
		return &pb.SimpleResponse{Id: "ok"}, nil
	}

	resp, err := runtime.UnaryCallWithRetry(context.Background(), pool, addr, call, &pb.SimpleRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != "ok" {
		t.Fatalf("expected id 'ok', got %q", resp.Id)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (retry after reconnect), got %d", callCount)
	}
}

func TestReconnectOnTransient_TransientError_ReconnectFails(t *testing.T) {
	pool := grpcx.NewPool()
	defer func() { _ = pool.Close() }()

	err := status.Error(codes.Unavailable, "down")
	conn, ok := runtime.ReconnectOnTransient(pool, "nonexistent-addr", err)
	if ok {
		t.Fatal("expected false when reconnect fails")
	}
	if conn != nil {
		t.Fatal("expected nil conn when reconnect fails")
	}
}

func TestReconnectOnTransient_TransientError_ReconnectSucceeds(t *testing.T) {
	pool := grpcx.NewPool()
	defer func() { _ = pool.Close() }()

	addr := "localhost:0"
	_, _ = pool.Connect(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))

	err := status.Error(codes.Unavailable, "down")
	conn, ok := runtime.ReconnectOnTransient(pool, addr, err)
	if !ok {
		t.Fatal("expected true for successful reconnect")
	}
	if conn == nil {
		t.Fatal("expected non-nil conn after successful reconnect")
	}
}
