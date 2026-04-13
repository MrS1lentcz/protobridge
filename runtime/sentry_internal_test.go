package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Internal tests exercise the package-private helpers `recoverGoroutine`
// and `isClientGone`, which are invoked from goroutines spawned by SSE/WS
// handlers and are otherwise hard to drive from external tests.

// recover() only works when invoked from a function that was deferred
// directly. recoverGoroutine is the deferred function in production code,
// so the test must call it the same way: `defer recoverGoroutine()` inside
// a goroutine that panics.

func TestRecoverGoroutine_PanicWithError(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverGoroutine()
		panic(errors.New("boom"))
	}()
	wg.Wait()
}

func TestRecoverGoroutine_PanicWithString(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverGoroutine()
		panic("string panic")
	}()
	wg.Wait()
}

func TestRecoverGoroutine_NoPanic(t *testing.T) {
	defer recoverGoroutine()
}

func TestIsClientGone(t *testing.T) {
	if isClientGone(nil) {
		t.Error("nil error must not be client-gone")
	}
	if !isClientGone(context.Canceled) {
		t.Error("context.Canceled must be client-gone")
	}
	if !isClientGone(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must be client-gone")
	}
	if !isClientGone(status.Error(codes.Canceled, "client gone")) {
		t.Error("gRPC Canceled must be client-gone")
	}
	if isClientGone(status.Error(codes.Internal, "boom")) {
		t.Error("gRPC Internal must not be client-gone")
	}
	if isClientGone(errors.New("plain error")) {
		t.Error("plain error must not be client-gone")
	}
}

func TestStringify(t *testing.T) {
	if got := stringify("x"); got != "x" {
		t.Errorf("stringify(string): got %q", got)
	}
	if got := stringify(errors.New("boom")); got != "boom" {
		t.Errorf("stringify(error): got %q", got)
	}
	if got := stringify(123); got != "unknown panic" {
		t.Errorf("stringify(other): got %q", got)
	}
}

func TestPanicError_Error(t *testing.T) {
	pe := &panicError{value: "hello"}
	if got := pe.Error(); got != "panic: hello" {
		t.Errorf("panicError: got %q", got)
	}
}
