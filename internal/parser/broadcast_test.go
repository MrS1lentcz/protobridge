package parser

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// withBroadcast attaches a BroadcastOptions extension to a ServiceOptions.
func withBroadcast(route string) *descriptorpb.ServiceOptions {
	so := &descriptorpb.ServiceOptions{}
	proto.SetExtension(so, optionspb.E_Broadcast, &optionspb.BroadcastOptions{Route: route})
	return so
}

// oneofIdx returns a pointer usable as OneofIndex.
func oneofIdx(i int32) *int32 { return &i }

// envelopeWithOneof builds a message whose sole declaration is a oneof of
// variants — one field per provided variant.
func envelopeWithOneof(name string, variants []struct{ field, typeName string }) *descriptorpb.DescriptorProto {
	fields := make([]*descriptorpb.FieldDescriptorProto, 0, len(variants))
	for _, v := range variants {
		fields = append(fields, &descriptorpb.FieldDescriptorProto{
			Name:       sp(v.field),
			Number:     i32p(int32(len(fields) + 1)),
			Type:       descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
			TypeName:   sp(v.typeName),
			OneofIndex: oneofIdx(0),
		})
	}
	return &descriptorpb.DescriptorProto{
		Name:      sp(name),
		Field:     fields,
		OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: sp("event")}},
	}
}

func eventMessage(name string) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name: sp(name),
		Options: withEvent(&optionspb.EventOptions{
			Kind:       optionspb.EventKind_BROADCAST,
			Visibility: optionspb.Visibility_PUBLIC,
		}),
	}
}

func broadcastService(name, route string, outputType string) *descriptorpb.ServiceDescriptorProto {
	return &descriptorpb.ServiceDescriptorProto{
		Name:    sp(name),
		Options: withBroadcast(route),
		Method: []*descriptorpb.MethodDescriptorProto{{
			Name:            sp("Stream"),
			InputType:       sp(".myapp.events.StreamRequest"),
			OutputType:      sp(outputType),
			ServerStreaming: bp(true),
		}},
	}
}

func TestParse_BroadcastService_Happy(t *testing.T) {
	req := makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			eventMessage("OrderCreated"),
			eventMessage("OrderShipped"),
			envelopeWithOneof("OrderEnvelope", []struct{ field, typeName string }{
				{"order_created", ".myapp.events.OrderCreated"},
				{"order_shipped", ".myapp.events.OrderShipped"},
			}),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			broadcastService("OrderBroadcast", "/api/v1/events/orders", ".myapp.events.OrderEnvelope"),
		},
	)
	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(api.BroadcastServices) != 1 {
		t.Fatalf("expected 1 broadcast service, got %d", len(api.BroadcastServices))
	}
	bs := api.BroadcastServices[0]
	if bs.Route != "/api/v1/events/orders" {
		t.Errorf("route: %q", bs.Route)
	}
	if len(bs.Events) != 2 {
		t.Fatalf("expected 2 oneof variants, got %d", len(bs.Events))
	}
	if bs.Events[0].Subject != "order_created" || bs.Events[1].Subject != "order_shipped" {
		t.Errorf("subjects: %+v", bs.Events)
	}
	// GoPackage on each BroadcastEvent mirrors its source Event.
	for _, be := range bs.Events {
		if be.GoPackage != bs.GoPackage {
			t.Errorf("BroadcastEvent.GoPackage = %q, want %q (same as service)", be.GoPackage, bs.GoPackage)
		}
	}
}

func TestParse_BroadcastService_Errors(t *testing.T) {
	// 1) route missing.
	req := makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			eventMessage("OrderCreated"),
			envelopeWithOneof("Envelope", []struct{ field, typeName string }{
				{"order_created", ".myapp.events.OrderCreated"},
			}),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "", ".myapp.events.Envelope"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "route") {
		t.Errorf("expected route error, got %v", err)
	}

	// 2) multiple methods.
	svc := broadcastService("Svc", "/x", ".myapp.events.Envelope")
	svc.Method = append(svc.Method, &descriptorpb.MethodDescriptorProto{
		Name: sp("Extra"), InputType: sp(".myapp.events.StreamRequest"),
		OutputType: sp(".myapp.events.Envelope"), ServerStreaming: bp(true),
	})
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			eventMessage("OrderCreated"),
			envelopeWithOneof("Envelope", []struct{ field, typeName string }{
				{"order_created", ".myapp.events.OrderCreated"},
			}),
		}, nil, []*descriptorpb.ServiceDescriptorProto{svc},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected exactly-one-method error, got %v", err)
	}

	// 3) method is unary — must be server-streaming.
	svc = broadcastService("Svc", "/x", ".myapp.events.Envelope")
	svc.Method[0].ServerStreaming = bp(false)
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			eventMessage("OrderCreated"),
			envelopeWithOneof("Envelope", []struct{ field, typeName string }{
				{"order_created", ".myapp.events.OrderCreated"},
			}),
		}, nil, []*descriptorpb.ServiceDescriptorProto{svc},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "server-streaming") {
		t.Errorf("expected server-streaming error, got %v", err)
	}

	// 4) envelope missing.
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
		}, nil, []*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "/x", ".myapp.events.Missing"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "envelope") {
		t.Errorf("expected envelope error, got %v", err)
	}

	// 5) envelope has no oneof.
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			{Name: sp("Envelope")}, // no oneof
		}, nil, []*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "/x", ".myapp.events.Envelope"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "oneof") {
		t.Errorf("expected oneof error, got %v", err)
	}

	// 6) oneof variant is not a message.
	scalarEnvelope := &descriptorpb.DescriptorProto{
		Name: sp("Envelope"),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: sp("kind"), Number: i32p(1),
			Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			OneofIndex: oneofIdx(0),
		}},
		OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: sp("event")}},
	}
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{{Name: sp("StreamRequest")}, scalarEnvelope},
		nil, []*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "/x", ".myapp.events.Envelope"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "must be a message") {
		t.Errorf("expected non-message-variant error, got %v", err)
	}

	// 7) oneof variant message has no (protobridge.event) annotation.
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			{Name: sp("Untagged")}, // not annotated
			envelopeWithOneof("Envelope", []struct{ field, typeName string }{
				{"untagged", ".myapp.events.Untagged"},
			}),
		}, nil, []*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "/x", ".myapp.events.Envelope"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "not annotated") {
		t.Errorf("expected not-annotated error, got %v", err)
	}

	// 8) INTERNAL visibility event forbidden in the envelope.
	internal := &descriptorpb.DescriptorProto{
		Name: sp("InternalEvt"),
		Options: withEvent(&optionspb.EventOptions{
			Kind: optionspb.EventKind_BROADCAST, Visibility: optionspb.Visibility_INTERNAL,
		}),
	}
	req = makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{Name: sp("StreamRequest")},
			internal,
			envelopeWithOneof("Envelope", []struct{ field, typeName string }{
				{"internal_evt", ".myapp.events.InternalEvt"},
			}),
		}, nil, []*descriptorpb.ServiceDescriptorProto{
			broadcastService("Svc", "/x", ".myapp.events.Envelope"),
		},
	)
	if _, err := Parse(req); err == nil || !strings.Contains(err.Error(), "INTERNAL") {
		t.Errorf("expected INTERNAL visibility error, got %v", err)
	}
}

func TestGetBroadcastOptions_NilAndAbsent(t *testing.T) {
	if _, ok := getBroadcastOptions(&descriptorpb.ServiceDescriptorProto{}); ok {
		t.Error("no Options → ok must be false")
	}
	if _, ok := getBroadcastOptions(&descriptorpb.ServiceDescriptorProto{Options: &descriptorpb.ServiceOptions{}}); ok {
		t.Error("Options without extension → ok must be false")
	}
}

// TestBuildBroadcastService_EnvelopeLostOneofVariant exercises the defensive
// branch that fires when a oneof variant name has no matching field on the
// envelope message. Reachable only via a malformed internal state; forced
// here with a hand-crafted MessageType to guard the invariant.
func TestBuildBroadcastService_EnvelopeLostOneofVariant(t *testing.T) {
	envelope := &MessageType{
		Name: "Envelope", FullName: ".x.Envelope",
		// Oneof declares one variant, but the corresponding Field is missing
		// from Fields[] — exactly the "lost track" condition.
		Fields:     nil,
		OneofDecls: []*OneofDecl{{Name: "event", Variants: []*OneofVariant{{FieldName: "orphan", IsMessage: true}}}},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: sp("Svc"),
		Method: []*descriptorpb.MethodDescriptorProto{{
			Name:            sp("Stream"),
			OutputType:      sp(".x.Envelope"),
			ServerStreaming: bp(true),
		}},
	}
	opts := &optionspb.BroadcastOptions{Route: "/x"}
	_, err := buildBroadcastService(svc, opts,
		"x", "example.com/x",
		map[string]*MessageType{".x.Envelope": envelope},
		map[string]*Event{},
	)
	if err == nil || !strings.Contains(err.Error(), "not annotated") {
		t.Errorf("orphan oneof variant should fall through to not-annotated error, got %v", err)
	}
}
