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
	return events.MarshalJSONEnvelope(subject, json.RawMessage(payload))
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

func TestMarshalJSONEnvelope_Shape(t *testing.T) {
	out, err := events.MarshalJSONEnvelope("x", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"subject":"x","event":{"a":1}}`
	if string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}
