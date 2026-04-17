package runtime_test

import (
	"context"
	"fmt"
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

func TestMetricsHandler_ReturnsHandler(t *testing.T) {
	// MetricsHandler returns a real promhttp handler.
	h := runtime.MetricsHandler()
	if h == nil {
		t.Fatal("expected non-nil handler from MetricsHandler")
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

func TestInitOTel_ShutdownReturnsFirstError(t *testing.T) {
	cfg := runtime.OTelConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "localhost:4317",
	}
	shutdown, err := runtime.InitOTel(t.Context(), cfg)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := shutdown(ctx); err == nil {
		t.Fatal("expected shutdown error with canceled context")
	}
}

func TestGracefulShutdownOTel_WithError(t *testing.T) {
	// OTelShutdown that returns an error.
	shutdown := runtime.OTelShutdown(func(_ context.Context) error {
		return fmt.Errorf("shutdown failed")
	})
	// Should not panic.
	runtime.GracefulShutdownOTel(shutdown)
}
