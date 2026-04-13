package eventsgen

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	parserpkg "github.com/mrs1lentcz/protobridge/internal/parser"
)

func TestParseOptions_Default(t *testing.T) {
	opts, err := ParseOptions("")
	if err != nil {
		t.Fatal(err)
	}
	if opts.OutputPkg != "events" {
		t.Errorf("default OutputPkg: %q", opts.OutputPkg)
	}
}

func TestParseOptions_Override(t *testing.T) {
	opts, err := ParseOptions("output_pkg=eventspkg")
	if err != nil {
		t.Fatal(err)
	}
	if opts.OutputPkg != "eventspkg" {
		t.Errorf("got %q", opts.OutputPkg)
	}
}

func TestParseOptions_UnknownKey(t *testing.T) {
	if _, err := ParseOptions("bogus=x"); err == nil {
		t.Fatal("expected error")
	}
}

func sampleAPI() *parserpkg.ParsedAPI {
	orderCreated := &parserpkg.MessageType{
		Name: "OrderCreated", FullName: ".myapp.events.OrderCreated",
		Fields: []*parserpkg.Field{
			{Name: "order_id", Type: 9}, // TYPE_STRING = 9
			{Name: "total_cents", Type: 5},
		},
	}
	orderShipped := &parserpkg.MessageType{
		Name: "OrderShipped", FullName: ".myapp.events.OrderShipped",
		Fields: []*parserpkg.Field{
			{Name: "order_id", Type: 9},
		},
	}
	internalOnly := &parserpkg.MessageType{
		Name: "InternalDebug", FullName: ".myapp.events.InternalDebug",
		Fields: []*parserpkg.Field{{Name: "msg", Type: 9}},
	}
	return &parserpkg.ParsedAPI{
		Messages: map[string]*parserpkg.MessageType{
			orderCreated.FullName: orderCreated,
			orderShipped.FullName: orderShipped,
			internalOnly.FullName: internalOnly,
		},
		Events: []*parserpkg.Event{
			{
				Message: orderCreated, Subject: "order_created",
				Kind: parserpkg.EventKindBroadcast, Visibility: parserpkg.EventVisibilityPublic,
				GoPackage: "example.com/myapp/events",
			},
			{
				Message: orderShipped, Subject: "orders.shipped",
				Kind: parserpkg.EventKindBoth, DurableGroup: "shipping-mailer",
				Visibility: parserpkg.EventVisibilityPublic,
				GoPackage:  "example.com/myapp/events",
			},
			{
				Message: internalOnly, Subject: "debug.events",
				Kind: parserpkg.EventKindDurable, Visibility: parserpkg.EventVisibilityInternal,
				GoPackage: "example.com/myapp/events",
			},
		},
	}
}

func TestGenerate_EmitsHelpersAndAsyncAPI(t *testing.T) {
	resp, err := Generate(sampleAPI(), Options{OutputPkg: "events"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	files := map[string]string{}
	for _, f := range resp.File {
		files[f.GetName()] = f.GetContent()
	}
	if _, ok := files["example_com_myapp_events_events.go"]; !ok {
		t.Fatalf("missing per-pkg events file; got %v", keys(files))
	}
	if _, ok := files["schema/asyncapi.json"]; !ok {
		t.Fatalf("missing asyncapi schema; got %v", keys(files))
	}

	go_ := files["example_com_myapp_events_events.go"]

	// Every event gets a SubjectXxx constant + EmitXxx.
	for _, want := range []string{
		`SubjectOrderCreated = "order_created"`,
		"func EmitOrderCreated(",
		`SubjectOrderShipped = "orders.shipped"`,
		"func EmitOrderShipped(",
		`SubjectInternalDebug = "debug.events"`,
		"func EmitInternalDebug(",
	} {
		if !strings.Contains(go_, want) {
			t.Errorf("missing %q in generated file", want)
		}
	}

	// Broadcast-only event (OrderCreated) has SubscribeBroadcast but no
	// load-balanced Subscribe.
	if !strings.Contains(go_, "func SubscribeBroadcastOrderCreated(") {
		t.Error("broadcast event must have SubscribeBroadcast helper")
	}
	if strings.Contains(go_, "func SubscribeOrderCreated(") {
		t.Error("pure broadcast event must NOT have load-balanced Subscribe (only durable/both)")
	}

	// Durable-only event (InternalDebug) has Subscribe but no broadcast.
	if !strings.Contains(go_, "func SubscribeInternalDebug(") {
		t.Error("durable event must have load-balanced Subscribe")
	}
	if strings.Contains(go_, "func SubscribeBroadcastInternalDebug(") {
		t.Error("pure durable event must NOT have broadcast Subscribe")
	}

	// Both event has both helpers.
	if !strings.Contains(go_, "func SubscribeOrderShipped(") || !strings.Contains(go_, "func SubscribeBroadcastOrderShipped(") {
		t.Error("BOTH event must have both Subscribe variants")
	}
	// Default group from annotation makes it into the source.
	if !strings.Contains(go_, `"shipping-mailer"`) {
		t.Error("durable_group from annotation should appear as default group")
	}

	// Generated file must be parseable Go (format.Source already ran inside
	// the generator; this guards against syntax regressions).
	if _, err := parser.ParseFile(token.NewFileSet(), "example_com_myapp_events_events.go", go_, parser.AllErrors); err != nil {
		t.Errorf("generated file is not parseable Go: %v\n%s", err, go_)
	}

	// AsyncAPI: valid JSON, contains every channel and message.
	var doc map[string]any
	if err := json.Unmarshal([]byte(files["schema/asyncapi.json"]), &doc); err != nil {
		t.Fatalf("asyncapi not valid JSON: %v", err)
	}
	if doc["asyncapi"] != "3.0.0" {
		t.Errorf("asyncapi version: %v", doc["asyncapi"])
	}
	channels := doc["channels"].(map[string]any)
	for _, want := range []string{"order_created", "orders.shipped", "debug.events"} {
		if _, ok := channels[want]; !ok {
			t.Errorf("missing channel %q in asyncapi; got %v", want, channels)
		}
	}
}

func TestGenerate_NoEventsReturnsEmptyResponse(t *testing.T) {
	resp, err := Generate(&parserpkg.ParsedAPI{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.File) != 0 {
		t.Errorf("expected no files for empty input, got %d", len(resp.File))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
