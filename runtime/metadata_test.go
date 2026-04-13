package runtime_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestSetUserMetadata(t *testing.T) {
	ctx := context.Background()
	userData := []byte("test-user-data")

	ctx = runtime.SetUserMetadata(ctx, userData)

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}

	vals := md.Get("x-protobridge-user")
	if len(vals) != 1 {
		t.Fatalf("expected 1 metadata value, got %d", len(vals))
	}

	// Should be base64 encoded.
	if vals[0] == string(userData) {
		t.Fatal("expected base64 encoded value, got raw")
	}
}

func TestSetUserMetadata_PreservesExisting(t *testing.T) {
	ctx := context.Background()
	md := metadata.New(map[string]string{"existing-key": "existing-value"})
	ctx = metadata.NewOutgoingContext(ctx, md)

	ctx = runtime.SetUserMetadata(ctx, []byte("user"))

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}
	if vals := md.Get("existing-key"); len(vals) != 1 || vals[0] != "existing-value" {
		t.Fatal("existing metadata should be preserved")
	}
	if vals := md.Get("x-protobridge-user"); len(vals) != 1 {
		t.Fatal("user metadata should be set")
	}
}

// TestGeneratedHandlerOrder_PreservesUserMetadata mirrors the call sequence
// emitted by internal/generator/handler.go: a fresh metadata.MD is built
// from path params/headers and applied via NewOutgoingContext first, then
// SetUserMetadata is called. The user metadata key must coexist with the
// path-param/header metadata in the resulting outgoing context.
func TestGeneratedHandlerOrder_PreservesUserMetadata(t *testing.T) {
	ctx := context.Background()

	md := metadata.MD{}
	md.Set("task_id", "42")
	ctx = metadata.NewOutgoingContext(ctx, md)

	ctx = runtime.SetUserMetadata(ctx, []byte("user"))

	out, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}
	if vals := out.Get("x-protobridge-user"); len(vals) != 1 {
		t.Fatalf("user metadata lost: got %v", out)
	}
	if vals := out.Get("task_id"); len(vals) != 1 || vals[0] != "42" {
		t.Fatalf("path-param metadata lost: got %v", out)
	}
}
