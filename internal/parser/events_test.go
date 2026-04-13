package parser

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// withEvent attaches an EventOptions extension to a MessageOptions.
func withEvent(opts *optionspb.EventOptions) *descriptorpb.MessageOptions {
	mo := &descriptorpb.MessageOptions{}
	proto.SetExtension(mo, optionspb.E_Event, opts)
	return mo
}

func TestParseEvents_AnnotatedMessageCollected(t *testing.T) {
	req := makeRequest("myapp.events", "events.proto",
		[]*descriptorpb.DescriptorProto{
			{
				Name: sp("OrderCreated"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: sp("order_id"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
				},
				Options: withEvent(&optionspb.EventOptions{
					Kind:        optionspb.EventKind_BROADCAST,
					Visibility:  optionspb.Visibility_PUBLIC,
					Description: "Emitted when a new order is placed.",
				}),
			},
			// Plain message — must NOT appear in api.Events.
			{Name: sp("Plain")},
		},
		nil, nil,
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(api.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(api.Events))
	}
	ev := api.Events[0]
	if ev.Subject != "order_created" {
		t.Errorf("default subject should be snake_case of message name; got %q", ev.Subject)
	}
	if ev.Kind != EventKindBroadcast {
		t.Errorf("kind: got %v", ev.Kind)
	}
	if ev.Visibility != EventVisibilityPublic {
		t.Errorf("visibility: got %v", ev.Visibility)
	}
	if ev.Description == "" {
		t.Error("description should be carried through")
	}
	// Message pointer must come from api.Messages so consumers see fields.
	if ev.Message == nil || ev.Message != api.Messages[".myapp.events.OrderCreated"] {
		t.Errorf("Event.Message should point into api.Messages: %+v", ev.Message)
	}
	if len(ev.Message.Fields) != 1 || ev.Message.Fields[0].Name != "order_id" {
		t.Errorf("Event.Message.Fields not populated: %+v", ev.Message)
	}
}

func TestParseEvents_ExplicitSubjectWins(t *testing.T) {
	req := makeRequest("x", "x.proto",
		[]*descriptorpb.DescriptorProto{
			{
				Name: sp("Shipped"),
				Options: withEvent(&optionspb.EventOptions{
					Subject: "shipments.shipped",
					Kind:    optionspb.EventKind_DURABLE,
				}),
			},
		},
		nil, nil,
	)
	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if api.Events[0].Subject != "shipments.shipped" {
		t.Errorf("explicit subject should override default: %q", api.Events[0].Subject)
	}
}

func TestParseEvents_NestedMessageAlsoCollected(t *testing.T) {
	req := makeRequest("x", "x.proto",
		[]*descriptorpb.DescriptorProto{
			{
				Name: sp("Outer"),
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name: sp("Inner"),
						Options: withEvent(&optionspb.EventOptions{
							Kind: optionspb.EventKind_BROADCAST,
						}),
					},
				},
			},
		},
		nil, nil,
	)
	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(api.Events) != 1 {
		t.Fatalf("expected nested event to be collected, got %d events", len(api.Events))
	}
	if api.Events[0].Subject != "inner" {
		t.Errorf("nested subject: %q", api.Events[0].Subject)
	}
}

func TestCamelToSnake_AcronymAware(t *testing.T) {
	cases := []struct{ in, want string }{
		{"OrderCreated", "order_created"},
		{"GitCreatePR", "git_create_pr"},
		{"HTTPRequest", "http_request"},
		{"Simple", "simple"},
	}
	for _, tc := range cases {
		if got := camelToSnake(tc.in); got != tc.want {
			t.Errorf("camelToSnake(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
