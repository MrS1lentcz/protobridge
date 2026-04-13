package mcp

import (
	"context"
	"net/http"
	"testing"
)

// HTTPHeadersFromContext's "value present" branch is unreachable from
// external tests because httpHeadersCtxKey is unexported — only an internal
// test can stash a value with the right key type. ServeStreamableHTTP wires
// this in production via WithHTTPContextFunc; the test here mirrors that
// wiring directly so the lookup is covered without booting an HTTP server.
func TestHTTPHeadersFromContext_ReturnsStashedHeaders(t *testing.T) {
	want := http.Header{
		"Authorization": []string{"Bearer abc"},
		"X-Session-Id":  []string{"sess-1"},
	}
	ctx := context.WithValue(context.Background(), httpHeadersCtxKey{}, want)

	got := HTTPHeadersFromContext(ctx)
	if got == nil {
		t.Fatal("expected stashed headers, got nil")
	}
	if got.Get("Authorization") != "Bearer abc" {
		t.Errorf("Authorization: got %q", got.Get("Authorization"))
	}
	if got.Get("X-Session-Id") != "sess-1" {
		t.Errorf("X-Session-Id: got %q", got.Get("X-Session-Id"))
	}
}

func TestHTTPHeadersFromContext_WrongValueTypeFallsThrough(t *testing.T) {
	// If something else stashes a non-Header value at the same key (only
	// possible from inside this package), the accessor must not panic.
	ctx := context.WithValue(context.Background(), httpHeadersCtxKey{}, "not a header")
	if got := HTTPHeadersFromContext(ctx); got != nil {
		t.Errorf("expected nil for wrong-type value, got %v", got)
	}
}
