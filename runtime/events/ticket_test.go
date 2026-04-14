package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

func TestMemoryTicketStore_IssueAndRedeem(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)

	labels := map[string]string{"tenant_id": "acme"}
	ticket, err := s.Issue(context.Background(), labels, time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ticket == "" {
		t.Fatal("empty ticket")
	}

	got, err := s.Redeem(context.Background(), ticket)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got["tenant_id"] != "acme" {
		t.Fatalf("labels mismatch: %v", got)
	}

	// One-shot: second redeem must fail.
	if _, err := s.Redeem(context.Background(), ticket); !errors.Is(err, events.ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid on replay, got %v", err)
	}
}

func TestMemoryTicketStore_Expiry(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)

	ticket, err := s.Issue(context.Background(), nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := s.Redeem(context.Background(), ticket); !errors.Is(err, events.ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid on expired ticket, got %v", err)
	}
}

func TestMemoryTicketStore_LabelsAreCopied(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)

	labels := map[string]string{"k": "v"}
	ticket, err := s.Issue(context.Background(), labels, time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	labels["k"] = "mutated" // should not leak into stored entry

	got, err := s.Redeem(context.Background(), ticket)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got["k"] != "v" {
		t.Fatalf("stored labels mutated: %v", got)
	}
}

func TestTicketIssuer_POSTReturnsTicket(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	handler := events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(r *http.Request) (map[string]string, error) {
			if r.Header.Get("Authorization") == "" {
				return nil, errors.New("no auth")
			}
			return map[string]string{"user_id": "u-1"}, nil
		},
		Store: store,
		TTL:   5 * time.Second,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer xyz")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Ticket == "" {
		t.Fatal("empty ticket in response")
	}
	if body.ExpiresIn != 5 {
		t.Fatalf("expires_in: %d", body.ExpiresIn)
	}

	got, err := store.Redeem(context.Background(), body.Ticket)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got["user_id"] != "u-1" {
		t.Fatalf("labels: %v", got)
	}
}

func TestTicketIssuer_UnauthorizedWhenPrincipalFails(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	handler := events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(*http.Request) (map[string]string, error) {
			return nil, errors.New("denied")
		},
		Store: store,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Post(srv.URL, "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestTicketIssuer_MethodNotAllowedOnGET(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	handler := events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(*http.Request) (map[string]string, error) { return nil, nil },
		Store:     store,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("allow: %q", resp.Header.Get("Allow"))
	}
}

// TestBroadcastHub_SSEStreamsEvents exercises the SSE transport branch end
// to end: request with Accept: text/event-stream is routed to the SSE
// handler, frames arrive as SSE events, labels are carried through.
func TestBroadcastHub_SSEStreamsEvents(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	subscribed := make(chan struct{}, 1)
	hub := events.NewBroadcastHub(hubCtx, events.WithOnSubscribed(
		events.BroadcastConfig{
			Source:            src,
			Marshal:           passthroughMarshaler,
			HeartbeatInterval: time.Hour, // keep the test deterministic
		},
		func() { subscribed <- struct{}{} },
	))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type: %q", ct)
	}

	awaitSubscribed(t, subscribed)
	src.frames <- events.BroadcastFrame{
		Subject: "task_created",
		Payload: []byte(`{"id":"t-1"}`),
	}

	// Read one SSE event: expect "event: message\ndata: <json>\n\n".
	block, err := readSSEBlock(resp.Body)
	if err != nil {
		t.Fatalf("read sse: %v", err)
	}
	if !strings.Contains(block, "event: message") {
		t.Fatalf("missing event line: %q", block)
	}
	dataLine := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatalf("missing data line: %q", block)
	}
	var env events.JSONEnvelope
	if err := json.Unmarshal([]byte(dataLine), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Subject != "task_created" {
		t.Fatalf("envelope: %+v", env)
	}
}

// TestBroadcastHub_TicketAuthBindsLabels verifies that redeeming a ticket
// binds the resulting hub client to the issuance-time labels, and the
// configured PrincipalLabels function is NOT consulted on the SSE path
// when ?ticket= is present.
func TestBroadcastHub_TicketAuthBindsLabels(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	ticket, err := store.Issue(context.Background(), map[string]string{"tenant_id": "acme"}, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	subscribed := make(chan struct{}, 1)
	hub := events.NewBroadcastHub(hubCtx, events.WithOnSubscribed(
		events.BroadcastConfig{
			Source:      src,
			Marshal:     passthroughMarshaler,
			TicketStore: store,
			PrincipalLabels: func(*http.Request) (map[string]string, error) {
				t.Fatal("PrincipalLabels must not run when ticket is present")
				return nil, nil
			},
			Matcher:           events.DefaultLabelMatcher,
			HeartbeatInterval: time.Hour,
		},
		func() { subscribed <- struct{}{} },
	))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"?ticket="+ticket, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	awaitSubscribed(t, subscribed)

	// Only the matching-label frame should arrive; the second is filtered.
	src.frames <- events.BroadcastFrame{Subject: "a", Payload: []byte(`{}`), Labels: map[string]string{"tenant_id": "acme"}}
	src.frames <- events.BroadcastFrame{Subject: "b", Payload: []byte(`{}`), Labels: map[string]string{"tenant_id": "other"}}
	src.frames <- events.BroadcastFrame{Subject: "c", Payload: []byte(`{}`), Labels: map[string]string{"tenant_id": "acme"}}

	got := []string{}
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < 2 && time.Now().Before(deadline) {
		block, err := readSSEBlock(resp.Body)
		if err != nil {
			t.Fatalf("read sse: %v", err)
		}
		if strings.HasPrefix(block, ":") { // heartbeat comment
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data: ") {
				var env events.JSONEnvelope
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &env); err == nil {
					got = append(got, env.Subject)
				}
			}
		}
	}
	if strings.Join(got, ",") != "a,c" {
		t.Fatalf("subjects: %v (expected a,c — tenant_id=other must be filtered)", got)
	}
}

// TestBroadcastHub_TicketInvalidReturns401 covers the unhappy path: a
// request with ?ticket=<unknown> must fail fast with 401 before any
// stream upgrade.
func TestBroadcastHub_TicketInvalidReturns401(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:      src,
		Marshal:     passthroughMarshaler,
		TicketStore: store,
		Logger:      newSilentLogger(),
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"?ticket=nope", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// nonFlushingWriter deliberately hides the underlying Flusher so
// serveSSE takes its "streaming unsupported" defensive branch.
type nonFlushingWriter struct {
	h    http.Header
	code int
	buf  bytes.Buffer
}

func (w *nonFlushingWriter) Header() http.Header {
	if w.h == nil {
		w.h = make(http.Header)
	}
	return w.h
}
func (w *nonFlushingWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *nonFlushingWriter) WriteHeader(code int)        { w.code = code }

// failingWriter returns errFailedWrite on every Write after the first N
// calls so tests can force the SSE write-error branches without needing
// to coordinate TCP disconnects with the server's event loop.
type failingWriter struct {
	h         http.Header
	failAfter int
	writes    int
	done      chan struct{}
}

var errFailedWrite = errors.New("write failed")

func newFailingWriter(failAfter int) *failingWriter {
	return &failingWriter{h: make(http.Header), failAfter: failAfter, done: make(chan struct{})}
}
func (w *failingWriter) Header() http.Header { return w.h }
func (w *failingWriter) WriteHeader(int)     {}
func (w *failingWriter) Flush()              {}
func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		select {
		case <-w.done:
		default:
			close(w.done)
		}
		return 0, errFailedWrite
	}
	return len(p), nil
}

func TestBroadcastHub_SSEReturnsOnHeartbeatWriteError(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:            src,
		Marshal:           passthroughMarshaler,
		HeartbeatInterval: 10 * time.Millisecond,
	})

	fw := newFailingWriter(0)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")

	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(fw, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveSSE did not return on heartbeat write error")
	}
}

func TestBroadcastHub_SSEReturnsOnWriteError(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	subscribed := make(chan struct{}, 1)
	hub := events.NewBroadcastHub(hubCtx, events.WithOnSubscribed(
		events.BroadcastConfig{
			Source:            src,
			Marshal:           passthroughMarshaler,
			HeartbeatInterval: time.Hour,
		},
		func() { subscribed <- struct{}{} },
	))

	fw := newFailingWriter(0) // every Write fails — first frame triggers exit
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")

	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(fw, req)
		close(done)
	}()

	awaitSubscribed(t, subscribed)
	src.frames <- events.BroadcastFrame{Subject: "a", Payload: []byte(`{}`)}
	src.frames <- events.BroadcastFrame{Subject: "b", Payload: []byte(`{}`)}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveSSE did not return on write error")
	}
}

// TestBroadcastHub_SSERejectsNonFlusherResponse covers the defensive
// branch where the ResponseWriter does not implement http.Flusher.
func TestBroadcastHub_SSERejectsNonFlusherResponse(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:  src,
		Marshal: passthroughMarshaler,
	})
	rw := &nonFlushingWriter{}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")
	hub.ServeHTTP(rw, req)
	if rw.code != http.StatusInternalServerError {
		t.Fatalf("status: %d", rw.code)
	}
}

// TestBroadcastHub_SSEExitsOnClientDisconnect forces the write-error
// branch in serveSSE by pushing frames faster than the client reads
// while the request is cancelled mid-flight.
func TestBroadcastHub_SSEExitsOnClientDisconnect(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	subscribed := make(chan struct{}, 1)
	hub := events.NewBroadcastHub(hubCtx, events.WithOnSubscribed(
		events.BroadcastConfig{
			Source:            src,
			Marshal:           passthroughMarshaler,
			HeartbeatInterval: 10 * time.Millisecond, // pump heartbeats
		},
		func() { subscribed <- struct{}{} },
	))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	reqCtx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	awaitSubscribed(t, subscribed)
	// Push a message so the message-write path runs at least once.
	src.frames <- events.BroadcastFrame{Subject: "x", Payload: []byte(`{}`)}
	_, _ = readSSEBlock(resp.Body)

	// Hard-close the client side; the server's next write (message or
	// heartbeat) will fail and serveSSE should return.
	cancel()
	_ = resp.Body.Close()
	// Nothing else to assert — just ensure we don't leak the handler.
	time.Sleep(50 * time.Millisecond)
}

func TestMemoryTicketStore_EmptyTicketRejected(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)
	if _, err := s.Redeem(context.Background(), ""); !errors.Is(err, events.ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid, got %v", err)
	}
}

func TestMemoryTicketStore_ReapEvictsExpired(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)

	live, _ := s.Issue(context.Background(), nil, time.Hour)
	dead, _ := s.Issue(context.Background(), nil, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	events.ReapExpired(s, time.Now())

	if _, err := s.Redeem(context.Background(), dead); !errors.Is(err, events.ErrTicketInvalid) {
		t.Fatalf("dead ticket should be gone: %v", err)
	}
	if _, err := s.Redeem(context.Background(), live); err != nil {
		t.Fatalf("live ticket must survive reap: %v", err)
	}
}

func TestMemoryTicketStore_ZeroTTLDefaultsTo30s(t *testing.T) {
	s := events.NewMemoryTicketStore()
	t.Cleanup(s.Close)
	// ttl <= 0 should not error out — the store applies a 30s default.
	ticket, err := s.Issue(context.Background(), nil, 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := s.Redeem(context.Background(), ticket); err != nil {
		t.Fatalf("redeem: %v", err)
	}
}

func TestTicketIssuer_PanicsOnMissingPrincipal(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = events.NewTicketIssuer(events.TicketIssuerConfig{Store: events.NewMemoryTicketStore()})
}

func TestTicketIssuer_PanicsOnMissingStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(*http.Request) (map[string]string, error) { return nil, nil },
	})
}

// errStore forces the issuer's Issue call to fail so we can cover the
// 500 response branch (unreachable with the in-memory store since it
// only fails on crypto/rand exhaustion).
type errStore struct{}

func (errStore) Issue(context.Context, map[string]string, time.Duration) (string, error) {
	return "", errors.New("store busted")
}
func (errStore) Redeem(context.Context, string) (map[string]string, error) {
	return nil, errors.New("store busted")
}

func TestTicketIssuer_StoreErrorReturns500(t *testing.T) {
	handler := events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(*http.Request) (map[string]string, error) { return nil, nil },
		Store:     errStore{},
		Logger:    newSilentLogger(),
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Post(srv.URL, "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestTicketIssuer_DefaultsApplied(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)
	// No TTL, no Logger: both branches fall through to defaults.
	handler := events.NewTicketIssuer(events.TicketIssuerConfig{
		Principal: func(*http.Request) (map[string]string, error) { return nil, nil },
		Store:     store,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Post(srv.URL, "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ExpiresIn != 30 {
		t.Fatalf("expected default 30s TTL, got %d", body.ExpiresIn)
	}
}

// TestBroadcastHub_SSEHeartbeatFires drives the ticker branch by
// configuring a very short heartbeat interval and reading the comment
// line from the stream.
func TestBroadcastHub_SSEHeartbeatFires(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	t.Cleanup(hubCancel)

	hub := events.NewBroadcastHub(hubCtx, events.BroadcastConfig{
		Source:            src,
		Marshal:           passthroughMarshaler,
		HeartbeatInterval: 20 * time.Millisecond,
	})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// First block should be a heartbeat since we pushed no frames.
	block, err := readSSEBlock(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(block, ":ping") {
		t.Fatalf("expected heartbeat, got %q", block)
	}
}

// TestBroadcastHub_SSEClosesWhenHubContextCancelled exercises
// linkHubLifetime's hub-context branch: cancelling the ctx passed to
// NewBroadcastHub must terminate in-flight SSE streams.
func TestBroadcastHub_SSEClosesWhenHubContextCancelled(t *testing.T) {
	src := newFakeSource()
	hubCtx, hubCancel := context.WithCancel(context.Background())

	subscribed := make(chan struct{}, 1)
	hub := events.NewBroadcastHub(hubCtx, events.WithOnSubscribed(
		events.BroadcastConfig{
			Source:            src,
			Marshal:           passthroughMarshaler,
			HeartbeatInterval: time.Hour,
		},
		func() { subscribed <- struct{}{} },
	))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	reqCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	awaitSubscribed(t, subscribed)
	hubCancel() // should propagate into the per-request ctx and close the stream.

	// Read until EOF — should happen well under the request deadline.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after hub context cancellation")
	}
}

// awaitSubscribed blocks until the hub's onSubscribed hook fires or the
// deadline elapses. A timeout here indicates a real bug (no client ever
// registered) — failing fast beats waiting for the global test deadline.
func awaitSubscribed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscription hook")
	}
}

// readSSEBlock reads one blank-line-terminated SSE frame.
func readSSEBlock(r io.Reader) (string, error) {
	var buf bytes.Buffer
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			buf.WriteByte(b[0])
			if buf.Len() >= 2 {
				tail := buf.Bytes()[buf.Len()-2:]
				if tail[0] == '\n' && tail[1] == '\n' {
					return buf.String(), nil
				}
			}
		}
		if err != nil {
			if buf.Len() > 0 && errors.Is(err, io.EOF) {
				return buf.String(), nil
			}
			return buf.String(), err
		}
	}
}
