package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// passthroughMarshaler wraps the raw payload as-is in the JSONEnvelope
// shape — enough for the runtime broadcast tests, which don't depend on
// proto encoding.
func passthroughMarshaler(subject string, payload []byte, _ map[string]string) ([]byte, error) {
	return events.MarshalJSONEnvelope(subject, json.RawMessage(payload), nil)
}

func TestBroadcast_DeliversBusEventsAsJSONEnvelopes(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		Bus:      bus,
		Subjects: []string{"orders.created"},
		Marshal:  passthroughMarshaler,
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow() //nolint:errcheck

	// Give the handler a tick to wire the subscription before publishing.
	time.Sleep(50 * time.Millisecond)

	if err := bus.Publish(ctx, "orders.created", json.RawMessage(`{"order_id":"o-1"}`),
		events.KindBroadcast, nil); err != nil {
		t.Fatalf("publish: %v", err)
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

func TestBroadcast_RefusesUnconfiguredHandler(t *testing.T) {
	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		// Bus + Marshal omitted on purpose.
		Subjects: []string{"x"},
	}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for misconfigured handler, got %d", resp.StatusCode)
	}
}

// failingBus implements events.Bus where SubscribeBroadcast always errors —
// drives the subscribe-error branch of NewBroadcastHandler.
type failingBus struct{}

func (b *failingBus) Publish(_ context.Context, _ string, _ []byte, _ events.Kind, _ map[string]string) error {
	return nil
}
func (b *failingBus) SubscribeBroadcast(_ string, _ events.Handler) (events.Subscription, error) {
	return nil, &subErr{}
}
func (b *failingBus) SubscribeDurable(_, _ string, _ events.Handler) (events.Subscription, error) {
	return nil, &subErr{}
}
func (b *failingBus) Close() error { return nil }

type subErr struct{}

func (e *subErr) Error() string { return "subscribe down" }

func TestBroadcast_MarshalErrorDropsMessage(t *testing.T) {
	// Marshal returning an error must not break the WS stream — the bad
	// message is dropped and subsequent good messages still flow through.
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	first := true
	marshaler := func(subject string, payload []byte, _ map[string]string) ([]byte, error) {
		if first {
			first = false
			return nil, &subErr{} // simulate one-time decode failure
		}
		return events.MarshalJSONEnvelope(subject, json.RawMessage(payload), nil)
	}

	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		Bus:      bus,
		Subjects: []string{"x"},
		Marshal:  marshaler,
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(50 * time.Millisecond)
	// First publish triggers a marshal error → dropped.
	_ = bus.Publish(ctx, "x", []byte(`{"a":1}`), events.KindBroadcast, nil)
	time.Sleep(50 * time.Millisecond)
	// Second publish marshals OK → arrives at the client.
	_ = bus.Publish(ctx, "x", []byte(`{"b":2}`), events.KindBroadcast, nil)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"b":2`) {
		t.Errorf("expected the second (good) message, got: %s", data)
	}
}

func TestBroadcast_SubscribeErrorClosesConnection(t *testing.T) {
	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		Bus:      &failingBus{},
		Subjects: []string{"x"},
		Marshal:  passthroughMarshaler,
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Acceptable — handler may close before handshake completes.
		return
	}
	defer conn.CloseNow() //nolint:errcheck
	// Server closed; Read returns with an error promptly.
	if _, _, err := conn.Read(ctx); err == nil {
		t.Error("expected read error after subscribe failure")
	}
}

func TestBroadcast_LabelFilteringOnlyDeliversMatchingEvents(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		Bus:      bus,
		Subjects: []string{"orders.created"},
		Marshal: func(subject string, payload []byte, headers map[string]string) ([]byte, error) {
			return events.MarshalJSONEnvelope(subject, json.RawMessage(payload), events.HeadersToLabels(headers))
		},
		// This connection is "logged in" as tenant abc — must never see
		// events tagged tenant_id=xyz.
		PrincipalLabels: func(_ *http.Request) (map[string]string, error) {
			return map[string]string{"tenant_id": "abc"}, nil
		},
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow() //nolint:errcheck

	time.Sleep(50 * time.Millisecond)

	// Foreign-tenant event — must be filtered out by the server-side matcher.
	pubCtx := events.WithLabels(ctx, "tenant_id", "xyz")
	_ = bus.Publish(pubCtx, "orders.created",
		json.RawMessage(`{"order_id":"foreign"}`),
		events.KindBroadcast,
		events.LabelsToHeaders(events.LabelsFromContext(pubCtx), nil),
	)
	// Matching-tenant event — must arrive.
	pubCtx = events.WithLabels(ctx, "tenant_id", "abc")
	_ = bus.Publish(pubCtx, "orders.created",
		json.RawMessage(`{"order_id":"mine"}`),
		events.KindBroadcast,
		events.LabelsToHeaders(events.LabelsFromContext(pubCtx), nil),
	)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"order_id":"mine"`) {
		t.Errorf("expected the tenant=abc event, got %s", data)
	}
	if strings.Contains(string(data), `"foreign"`) {
		t.Error("foreign-tenant event must be filtered out by the server-side label matcher")
	}
	if !strings.Contains(string(data), `"tenant_id":"abc"`) {
		t.Errorf("envelope must carry labels for client-side filtering: %s", data)
	}
}

func TestBroadcast_PrincipalLabelsAuthFailureRejectsBeforeUpgrade(t *testing.T) {
	bus := events.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	srv := httptest.NewServer(events.NewBroadcastHandler(events.BroadcastConfig{
		Bus:      bus,
		Subjects: []string{"x"},
		Marshal:  passthroughMarshaler,
		PrincipalLabels: func(_ *http.Request) (map[string]string, error) {
			return nil, &subErr{}
		},
	}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on PrincipalLabels error, got %d", resp.StatusCode)
	}
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
