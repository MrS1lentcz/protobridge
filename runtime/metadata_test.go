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
