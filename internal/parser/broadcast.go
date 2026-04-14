package parser

import (
	"fmt"
	"strings"

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
//  7. The envelope may optionally carry one map<string,string> field named
//     "labels" alongside the oneof. Backend bus→stream adapter populates it
//     from publish headers; gateway uses it for per-principal filtering and
//     forwards it in the WS JSON envelope. No other fields are permitted.
func buildBroadcastService(
	svc *descriptorpb.ServiceDescriptorProto,
	opts *optionspb.BroadcastOptions,
	protoPkg, goPkg string,
	messages map[string]*MessageType,
	eventByFQN map[string]*Event,
) (*BroadcastService, error) {
	route := opts.GetRoute()
	if route == "" {
		return nil, fmt.Errorf("broadcast: route must be set")
	}
	if !strings.HasPrefix(route, "/") {
		return nil, fmt.Errorf("broadcast: route %q must start with %q — generators emit it verbatim as the HTTP mount path", route, "/")
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
	// The streaming RPC carries no caller parameters — it's a system fan-out
	// stream opened once per gateway pod. Require google.protobuf.Empty so
	// codegen can pick a single, well-known request type without plumbing
	// arbitrary import paths through the broadcast generator.
	if m.GetInputType() != ".google.protobuf.Empty" {
		return nil, fmt.Errorf("broadcast: %s.%s must take google.protobuf.Empty as input (got %s) — broadcast streams are parameterless", svc.GetName(), m.GetName(), m.GetInputType())
	}
	if len(envelope.OneofDecls) != 1 {
		return nil, fmt.Errorf("broadcast: envelope %s must contain exactly one oneof declaration, got %d", envelope.Name, len(envelope.OneofDecls))
	}
	// Envelope may contain only oneof variants plus an optional
	// `labels` map<string,string> field. Anything else would be silently
	// dropped by codegen, so fail fast with a clear message.
	var labelsField *Field
	for _, f := range envelope.Fields {
		if f.OneofIndex != nil {
			continue
		}
		if isLabelsMapField(f, messages) {
			if labelsField != nil {
				return nil, fmt.Errorf("broadcast: envelope %s declares more than one labels map — only one is permitted", envelope.Name)
			}
			labelsField = f
			continue
		}
		return nil, fmt.Errorf("broadcast: envelope %s field %q must be part of the oneof or the optional `labels` map<string,string> — extra fields are not supported", envelope.Name, f.Name)
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
		MethodName:   m.GetName(),
		Route:        opts.GetRoute(),
		GoPackage:    goPkg,
		ProtoPackage: protoPkg,
		Envelope:     envelope,
		LabelsField:  labelsField,
		Events:       events,
	}, nil
}

// isLabelsMapField reports whether f is a `map<string,string> labels` field
// — i.e. a repeated MESSAGE field of type EntryMessage with MapEntry=true and
// two STRING fields (key, value). The field name must be "labels" so accidental
// maps elsewhere on the envelope still fail validation with a clear error.
func isLabelsMapField(f *Field, messages map[string]*MessageType) bool {
	if f.Name != "labels" {
		return false
	}
	if f.Type != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE || !f.Repeated {
		return false
	}
	entry, ok := messages[f.TypeName]
	if !ok || entry == nil || !entry.MapEntry {
		return false
	}
	if len(entry.Fields) != 2 {
		return false
	}
	var key, val *Field
	for _, ef := range entry.Fields {
		switch ef.Name {
		case "key":
			key = ef
		case "value":
			val = ef
		}
	}
	if key == nil || val == nil {
		return false
	}
	return key.Type == descriptorpb.FieldDescriptorProto_TYPE_STRING &&
		val.Type == descriptorpb.FieldDescriptorProto_TYPE_STRING
}
