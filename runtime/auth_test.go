package runtime_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestNoAuth_ReturnsNilData(t *testing.T) {
	fn := runtime.NoAuth()
	r := httptest.NewRequest("GET", "/", nil)

	data, err := fn(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil data, got %v", data)
	}
}

func TestNewAuthFunc_ReturnsError(t *testing.T) {
	fn := runtime.NewAuthFunc(nil)
	r := httptest.NewRequest("GET", "/", nil)

	_, err := fn(context.Background(), r)
	if err == nil {
		t.Fatal("expected error from placeholder NewAuthFunc")
	}
	if err.Error() != "auth not configured" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// mockAuthCaller implements runtime.AuthCaller for testing.
type mockAuthCaller struct {
	resp proto.Message
	err  error
}

func (m *mockAuthCaller) CallAuth(ctx context.Context, headers map[string]string) (proto.Message, error) {
	return m.resp, m.err
}

func TestMakeAuthFunc_Success(t *testing.T) {
	resp := &pb.SimpleResponse{Id: "user-123", Name: "alice"}
	caller := &mockAuthCaller{resp: resp}

	fn := runtime.MakeAuthFunc(caller)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer token123")

	data, err := fn(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}

	// Verify we can unmarshal the returned bytes back into the proto.
	got := &pb.SimpleResponse{}
	if err := proto.Unmarshal(data, got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.Id != "user-123" || got.Name != "alice" {
		t.Fatalf("unexpected response: %v", got)
	}
}

func TestMakeAuthFunc_CallerError(t *testing.T) {
	caller := &mockAuthCaller{err: fmt.Errorf("auth service down")}

	fn := runtime.MakeAuthFunc(caller)
	r := httptest.NewRequest("GET", "/", nil)

	_, err := fn(context.Background(), r)
	if err == nil {
		t.Fatal("expected error from failing caller")
	}
	if err.Error() != "auth service down" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMakeAuthFunc_HeadersForwarded(t *testing.T) {
	var capturedHeaders map[string]string
	caller := &mockAuthCaller{
		resp: &pb.SimpleResponse{Id: "1"},
	}
	// Override CallAuth to capture headers.
	callerWithCapture := &headerCaptureCaller{
		resp:    &pb.SimpleResponse{Id: "1"},
		capture: &capturedHeaders,
	}

	fn := runtime.MakeAuthFunc(callerWithCapture)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer xyz")
	r.Header.Set("X-Request-Id", "req-456")

	_, err := fn(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = caller // suppress unused
	if capturedHeaders["Authorization"] != "Bearer xyz" {
		t.Fatalf("expected Authorization header, got %v", capturedHeaders)
	}
	if capturedHeaders["X-Request-Id"] != "req-456" {
		t.Fatalf("expected X-Request-Id header, got %v", capturedHeaders)
	}
}

type headerCaptureCaller struct {
	resp    proto.Message
	capture *map[string]string
}

func (h *headerCaptureCaller) CallAuth(ctx context.Context, headers map[string]string) (proto.Message, error) {
	*h.capture = headers
	return h.resp, nil
}
