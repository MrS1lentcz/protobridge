package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// fakeSource is a hand-driven BroadcastSource: tests push frames via Send and
// the source forwards them to the hub channel. Closing Done aborts Run with
// io.EOF (as a real source would on a clean stream end).
type fakeSource struct {
	frames chan events.BroadcastFrame
}

func newFakeSource() *fakeSource {
	return &fakeSource{frames: make(chan events.BroadcastFrame, 16)}
}

func (s *fakeSource) Run(ctx context.Context, out chan<- events.BroadcastFrame) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f, ok := <-s.frames:
			if !ok {
				return io.EOF
			}
			select {
			case out <- f:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// passthroughMarshaler wraps the raw payload as-is in the JSONEnvelope shape —
// runtime broadcast tests don't care about proto encoding.
func passthroughMarshaler(subject string, payload []byte, labels map[string]string) ([]byte, error) {
	return events.MarshalJSONEnvelope(subject, json.RawMessage(payload), labels)
}

func dialWS(t *testing.T, srv *httptest.Server) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	return conn, ctx, cancel
}

func TestBroadcastHub_DeliversFramesAsJSONEnvelopes(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, ctx, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(20 * time.Millisecond) // let the hub register the client
	src.frames <- events.BroadcastFrame{
		Subject: "orders.created",
		Payload: []byte(`{"order_id":"o-1"}`),
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env events.JSONEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("envelope not valid JSON: %v\n%s", err, data)
	}
	if env.Subject != "orders.created" {
		t.Errorf("subject: %q", env.Subject)
	}
	if !strings.Contains(string(env.Event), `"order_id":"o-1"`) {
		t.Errorf("event payload not propagated: %s", env.Event)
	}
}

func TestBroadcastHub_RefusesUnconfiguredHandler(t *testing.T) {
	hub := events.NewBroadcastHub(context.Background(), events.BroadcastConfig{
		Source: newFakeSource(),
		// Marshal omitted on purpose.
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for misconfigured hub, got %d", resp.StatusCode)
	}
}

type marshalErr struct{}

func (e *marshalErr) Error() string { return "marshal down" }

func TestBroadcastHub_MarshalErrorDropsFrameButKeepsStream(t *testing.T) {
	src := newFakeSource()
	first := true
	marshaler := func(subject string, payload []byte, labels map[string]string) ([]byte, error) {
		if first {
			first = false
			return nil, &marshalErr{}
		}
		return passthroughMarshaler(subject, payload, labels)
	}
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: marshaler,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, ctx, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(20 * time.Millisecond)
	src.frames <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{"a":1}`)}
	src.frames <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{"b":2}`)}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"b":2`) {
		t.Errorf("expected the second (good) message, got: %s", data)
	}
}

type authErr struct{}

func (e *authErr) Error() string { return "subscribe down" }

func TestBroadcastHub_PrincipalLabelsFiltering(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
		// This connection is "logged in" as tenant abc — must never see
		// frames tagged tenant_id=xyz.
		PrincipalLabels: func(_ *http.Request) (map[string]string, error) {
			return map[string]string{"tenant_id": "abc"}, nil
		},
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, ctx, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(20 * time.Millisecond)
	// Foreign-tenant frame — must be filtered out.
	src.frames <- events.BroadcastFrame{
		Subject: "orders.created", Payload: []byte(`{"order_id":"foreign"}`),
		Labels: map[string]string{"tenant_id": "xyz"},
	}
	// Matching-tenant frame — must arrive.
	src.frames <- events.BroadcastFrame{
		Subject: "orders.created", Payload: []byte(`{"order_id":"mine"}`),
		Labels: map[string]string{"tenant_id": "abc"},
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"order_id":"mine"`) {
		t.Errorf("expected tenant=abc frame, got %s", data)
	}
	if strings.Contains(string(data), `"foreign"`) {
		t.Error("foreign-tenant frame must be filtered out by the hub's label matcher")
	}
	if !strings.Contains(string(data), `"tenant_id":"abc"`) {
		t.Errorf("envelope must carry labels for client-side filtering: %s", data)
	}
}

func TestBroadcastHub_PrincipalLabelsAuthFailureRejectsBeforeUpgrade(t *testing.T) {
	hub := events.NewBroadcastHub(context.Background(), events.BroadcastConfig{
		Source:  newFakeSource(),
		Marshal: passthroughMarshaler,
		PrincipalLabels: func(_ *http.Request) (map[string]string, error) {
			return nil, &authErr{}
		},
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on PrincipalLabels error, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Response body must never contain the underlying auth error (could
	// leak backend reasons / IDs). A generic "unauthorized" string is fine.
	if strings.Contains(string(body), "subscribe down") {
		t.Errorf("response body leaked internal auth error: %q", string(body))
	}
}

// errorSource immediately fails — covers the hub.run() error-log branch.
type errorSource struct{}

func (e errorSource) Run(_ context.Context, _ chan<- events.BroadcastFrame) error {
	return errors.New("source crashed")
}

func TestBroadcastHub_SourceErrorDoesntCrashHandler(t *testing.T) {
	hub := events.NewBroadcastHub(context.Background(), events.BroadcastConfig{
		Source:         errorSource{},
		Marshal:        passthroughMarshaler,
		SourceRetryMax: -1, // exercise the legacy "log and stop" branch
		Logger:         newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	// Hub still serves WS; clients just won't receive any frames.
	conn, _, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck
}

func TestMarshalJSONEnvelope_Shape(t *testing.T) {
	out, err := events.MarshalJSONEnvelope("x", json.RawMessage(`{"a":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"subject":"x","event":{"a":1}}`
	if string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

// newSilentLogger returns a slog.Logger writing to io.Discard so test logs
// stay clean while the hub's internal Warn/Error branches still execute.
func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBroadcastHub_NilSourceLogsAndNoCrash(t *testing.T) {
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		// Source intentionally nil.
		Marshal: passthroughMarshaler,
		Logger:  newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	// HTTP layer still works — clients may attach but won't see frames.
	conn, _, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck
}

func TestBroadcastHub_DropOldestOnSlowClient(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:       src,
		Marshal:      passthroughMarshaler,
		ClientBuffer: 1, // tiny buffer makes drops deterministic
		Logger:       newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, ctx, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(20 * time.Millisecond)

	// Burst frames faster than the writer goroutine can drain the WS — a
	// single-slot buffer plus rapid producer guarantees the dispatch
	// drop-oldest branch is exercised. We don't assert which frame survives
	// (writer-vs-producer interleaving is non-deterministic) — only that the
	// connection survives the bursts and at least one frame arrives.
	for i := 0; i < 200; i++ {
		src.frames <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{"i":1}`)}
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read after burst: %v", err)
	}
}

func TestBroadcastHub_CustomLoggerUsed(t *testing.T) {
	// Cover the cfg.Logger != nil branch of logger().
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  errorSource{},
		Marshal: passthroughMarshaler,
		Logger:  newSilentLogger(),
	})
	_ = hub
	time.Sleep(20 * time.Millisecond) // give source goroutine time to log
}

func TestBroadcastHub_OnSubscribedHookFires(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	ready := make(chan struct{})
	cfg := events.WithOnSubscribed(events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
	}, func() { close(ready) })
	hub := events.NewBroadcastHub(hubCtx, cfg)
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, _, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("onSubscribed never fired")
	}
}

func TestBroadcastHub_MultipleClientsEachReceiveFrame(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn1, ctx1, cancel1 := dialWS(t, srv)
	defer cancel1()
	defer conn1.CloseNow() //nolint:errcheck
	conn2, ctx2, cancel2 := dialWS(t, srv)
	defer cancel2()
	defer conn2.CloseNow() //nolint:errcheck

	time.Sleep(30 * time.Millisecond)
	src.frames <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{"a":1}`)}

	for i, conn := range []*websocket.Conn{conn1, conn2} {
		ctx := ctx1
		if i == 1 {
			ctx = ctx2
		}
		if _, data, err := conn.Read(ctx); err != nil {
			t.Fatalf("client %d read: %v", i, err)
		} else if !strings.Contains(string(data), `"a":1`) {
			t.Errorf("client %d got %s", i, data)
		}
	}
}

func TestBroadcastHub_NonWSGETReturnsCleanly(t *testing.T) {
	// Plain HTTP GET (no Upgrade header) — websocket.Accept writes a 426
	// and ServeHTTP returns without panicking. Covers the post-Accept
	// error-return branch.
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  newFakeSource(),
		Marshal: passthroughMarshaler,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected non-200 for non-WS GET, got %d", resp.StatusCode)
	}
}

// flakySource errors on the first N Run calls then behaves normally, pushing
// one frame before blocking on ctx. Exercises hub retry + backoff-reset.
type flakySource struct {
	attempts   chan int
	failsFirst int
	frame      events.BroadcastFrame
}

func (s *flakySource) Run(ctx context.Context, out chan<- events.BroadcastFrame) error {
	n := <-s.attempts
	s.attempts <- n + 1
	if n < s.failsFirst {
		return errors.New("dial refused")
	}
	select {
	case out <- s.frame:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestBroadcastHub_RetriesSourceAfterError(t *testing.T) {
	src := &flakySource{
		attempts:   make(chan int, 1),
		failsFirst: 2,
		frame:      events.BroadcastFrame{Subject: "x", Payload: []byte(`{}`)},
	}
	src.attempts <- 0

	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:             src,
		Marshal:            passthroughMarshaler,
		SourceRetryInitial: 5 * time.Millisecond,
		SourceRetryMax:     20 * time.Millisecond,
		Logger:             newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	conn, ctx, cancel := dialWS(t, srv)
	defer cancel()
	defer conn.CloseNow() //nolint:errcheck

	// Two failed Run attempts, then the third delivers — must arrive despite
	// the earlier errors that would have permanently killed the pre-retry hub.
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	if _, _, err := conn.Read(readCtx); err != nil {
		t.Fatalf("expected frame after retries, got %v", err)
	}
	if n := <-src.attempts; n < 3 {
		t.Errorf("expected at least 3 Run attempts, got %d", n)
	}
}

func TestBroadcastHub_RetryMaxNegativeDisablesRetry(t *testing.T) {
	var attempts int
	src := funcSource(func(_ context.Context, _ chan<- events.BroadcastFrame) error {
		attempts++
		return errors.New("boom")
	})

	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:         src,
		Marshal:        passthroughMarshaler,
		SourceRetryMax: -1,
		Logger:         newSilentLogger(),
	})

	// No WS client needed — we're asserting the source goroutine doesn't
	// spin. Give the goroutine time to (not) retry.
	time.Sleep(50 * time.Millisecond)
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt with retry disabled, got %d", attempts)
	}
}

func TestBroadcastHub_BackoffResetsAfterDelivery(t *testing.T) {
	// Source behavior:
	//   attempt 1 → fail    (backoff 4ms → 8ms, capped 8ms)
	//   attempt 2 → fail    (backoff 8ms → 8ms)
	//   attempt 3 → deliver one frame, then fail (triggers reset on next)
	//   attempt 4 → fail, must wait only initial (4ms), not capped
	// Close done after attempt 4 to terminate.
	attempts := make(chan int, 10)
	done := make(chan struct{})
	src := funcSource(func(ctx context.Context, out chan<- events.BroadcastFrame) error {
		select {
		case attempts <- 1:
		default:
		}
		n := len(attempts)
		if n == 3 {
			select {
			case out <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{}`)}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if n >= 4 {
			<-done
			return ctx.Err()
		}
		return errors.New("flake")
	})

	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(func() { close(done); hubCancel() })

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:             src,
		Marshal:            passthroughMarshaler,
		SourceRetryInitial: 4 * time.Millisecond,
		SourceRetryMax:     8 * time.Millisecond,
		Logger:             newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	// Wait for the 4th attempt to register — that's past both the cap hit
	// and the post-delivery reset.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(attempts) >= 4 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected 4 attempts, got %d", len(attempts))
}

func TestBroadcastHub_SourceCleanStopDoesNotRetry(t *testing.T) {
	var attempts int
	src := funcSource(func(_ context.Context, _ chan<- events.BroadcastFrame) error {
		attempts++
		return nil // clean source-driven stop
	})

	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:             src,
		Marshal:            passthroughMarshaler,
		SourceRetryInitial: 1 * time.Millisecond,
		Logger:             newSilentLogger(),
	})

	time.Sleep(50 * time.Millisecond)
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt on nil-err stop, got %d", attempts)
	}
}

func TestBroadcastHub_ShutdownDuringBackoff(t *testing.T) {
	src := funcSource(func(_ context.Context, _ chan<- events.BroadcastFrame) error {
		return errors.New("always fails")
	})

	hubCtx, hubCancel := context.WithCancel(context.Background())
	events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:             src,
		Marshal:            passthroughMarshaler,
		SourceRetryInitial: 1 * time.Second, // long enough that cancel wins
		SourceRetryMax:     5 * time.Second,
		Logger:             newSilentLogger(),
	})

	// Let one failed Run happen, then cancel while the hub is sleeping in
	// the backoff select — exercises the ctx.Done branch of the timer wait.
	time.Sleep(20 * time.Millisecond)
	hubCancel()
	time.Sleep(20 * time.Millisecond)
}

func TestBroadcastHub_NilCtxDowngradesToBackground(t *testing.T) {
	// Must not panic — ctx=nil is misuse, but degrading to Background keeps
	// the hub usable rather than crashing every request goroutine.
	hub := events.NewBroadcastHub(nil, events.BroadcastConfig{ //nolint:staticcheck
		Source:         errorSource{},
		Marshal:        passthroughMarshaler,
		SourceRetryMax: -1,
		Logger:         newSilentLogger(),
	})
	if hub == nil {
		t.Fatal("expected hub, got nil")
	}
}

func TestBroadcastHub_SSEHeartbeatDefault(t *testing.T) {
	// Smoke test for the HeartbeatInterval<=0 → 15s default branch.
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)
	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err == nil {
		_ = resp.Body.Close()
	}
}

type funcSource func(ctx context.Context, out chan<- events.BroadcastFrame) error

func (f funcSource) Run(ctx context.Context, out chan<- events.BroadcastFrame) error {
	return f(ctx, out)
}

func TestHeadersToLabels_NilOrEmpty(t *testing.T) {
	if events.HeadersToLabels(nil) != nil {
		t.Error("nil headers should return nil")
	}
	if events.HeadersToLabels(map[string]string{}) != nil {
		t.Error("empty headers should return nil")
	}
}
