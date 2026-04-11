package runtime_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestOTelMiddleware_ReturnsHandler(t *testing.T) {
	mw := runtime.OTelMiddleware("test-service")
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}

	// Call the returned middleware to exercise the inner function.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)
	if handler == nil {
		t.Fatal("expected non-nil handler from middleware")
	}
}

func TestMetricsHandler_ReturnsNil(t *testing.T) {
	// MetricsHandler is a placeholder that returns nil.
	h := runtime.MetricsHandler()
	if h != nil {
		t.Fatal("expected nil handler from placeholder")
	}
}

func TestRecordConnectionOpenClose(t *testing.T) {
	// These should not panic.
	runtime.RecordConnectionOpen("ws")
	runtime.RecordConnectionClose("ws")
	runtime.RecordConnectionOpen("sse")
	runtime.RecordConnectionClose("sse")
}

func TestGracefulShutdownOTel_NilShutdown(t *testing.T) {
	// Should not panic with nil shutdown.
	runtime.GracefulShutdownOTel(nil)
}

func TestGracefulShutdownOTel_WithShutdown(t *testing.T) {
	called := false
	shutdown := runtime.OTelShutdown(func(_ context.Context) error {
		called = true
		return nil
	})
	runtime.GracefulShutdownOTel(shutdown)
	if !called {
		t.Fatal("expected shutdown to be called")
	}
}

func TestInitOTel_NoEndpoint(t *testing.T) {
	// InitOTel with empty endpoint should still succeed (no trace exporter).
	cfg := runtime.OTelConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "",
	}
	shutdown, err := runtime.InitOTel(t.Context(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown func")
	}
	// Clean up.
	runtime.GracefulShutdownOTel(shutdown)
}

func TestInitOTel_WithEndpoint(t *testing.T) {
	// InitOTel with an OTLP endpoint (connection is lazy, so this succeeds).
	cfg := runtime.OTelConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "localhost:4317",
	}
	shutdown, err := runtime.InitOTel(t.Context(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown func")
	}
	// Shutdown should not error even if collector is unreachable.
	runtime.GracefulShutdownOTel(shutdown)
}
