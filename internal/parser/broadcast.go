package parser

import (
	"fmt"

	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// buildBroadcastService validates a (protobridge.broadcast)-annotated
// service descriptor and returns the parsed BroadcastService entry.
//
// Shape contract (enforced here so generators can rely on it):
//  1. The service has exactly one method.
//  2. The method is server-streaming (client_streaming=false, server_streaming=true).
//  3. The method's return type is a message in api.Messages.
//  4. That envelope message has exactly one oneof declaration.
//  5. Every oneof variant's type is a message annotated with (protobridge.event).
//  6. Visibility on each event is PUBLIC (INTERNAL events must not be exposed).
func buildBroadcastService(
	svc *descriptorpb.ServiceDescriptorProto,
	opts *optionspb.BroadcastOptions,
	protoPkg, goPkg string,
	messages map[string]*MessageType,
	eventByFQN map[string]*Event,
) (*BroadcastService, error) {
	if opts.GetRoute() == "" {
		return nil, fmt.Errorf("broadcast: route must be set")
	}
	if len(svc.Method) != 1 {
		return nil, fmt.Errorf("broadcast: service must define exactly one streaming RPC, got %d methods", len(svc.Method))
	}
	m := svc.Method[0]
	if m.GetClientStreaming() || !m.GetServerStreaming() {
		return nil, fmt.Errorf("broadcast: %s.%s must be a server-streaming RPC", svc.GetName(), m.GetName())
	}

	envelope, ok := messages[m.GetOutputType()]
	if !ok || envelope == nil {
		return nil, fmt.Errorf("broadcast: envelope type %s not found in proto request", m.GetOutputType())
	}
	if len(envelope.OneofDecls) != 1 {
		return nil, fmt.Errorf("broadcast: envelope %s must contain exactly one oneof declaration, got %d", envelope.Name, len(envelope.OneofDecls))
	}

	var (
		variants = envelope.OneofDecls[0].Variants
		events   []*BroadcastEvent
	)
	for _, v := range variants {
		if !v.IsMessage {
			return nil, fmt.Errorf("broadcast: envelope %s oneof variant %s must be a message type", envelope.Name, v.FieldName)
		}
		// Find the typed message via its short name on the envelope's fields
		// — the parser already populated FullName on the field when walking
		// the message descriptor. An empty fieldFQN falls through to the
		// "not annotated" error below (eventByFQN lookup fails on ""),
		// keeping the code path linear.
		var fieldFQN string
		for _, f := range envelope.Fields {
			if f.Name == v.FieldName {
				fieldFQN = f.TypeName
				break
			}
		}
		ev, ok := eventByFQN[fieldFQN]
		if !ok {
			return nil, fmt.Errorf("broadcast: oneof variant %s (%s) is not annotated with (protobridge.event)", v.FieldName, fieldFQN)
		}
		if ev.Visibility == EventVisibilityInternal {
			return nil, fmt.Errorf("broadcast: oneof variant %s carries INTERNAL visibility — INTERNAL events must not be exposed via broadcast", v.FieldName)
		}
		events = append(events, &BroadcastEvent{
			OneofFieldName: v.FieldName,
			Message:        ev.Message,
			Subject:        ev.Subject,
			Visibility:     ev.Visibility,
			GoPackage:      ev.GoPackage,
		})
	}

	return &BroadcastService{
		Name:         svc.GetName(),
		Route:        opts.GetRoute(),
		GoPackage:    goPkg,
		ProtoPackage: protoPkg,
		Envelope:     envelope,
		Events:       events,
	}, nil
}
