package runtime_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
