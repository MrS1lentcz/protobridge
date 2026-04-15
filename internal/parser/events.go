package parser

import (
	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

type eventCollectCtx struct {
	pkg       string // proto package, e.g. "myapp.events"
	goPackage string // resolved Go import path
	prefix    string // current FQN prefix, walked into nested messages
}

// collectEvents walks msg (and its nested types) and appends an Event entry
// to api.Events for every message annotated with (protobridge.event). The
// MessageType pointer on the Event is taken from api.Messages so consumers
// see the fully-resolved field list.
func collectEvents(api *ParsedAPI, c *eventCollectCtx, msg *descriptorpb.DescriptorProto) {
	fqn := c.prefix + "." + msg.GetName()
	if opts, ok := getEventOptions(msg); ok {
		ev := &Event{
			Message:        api.Messages[fqn],
			Subject:        resolveEventSubject(opts.GetSubject(), msg.GetName()),
			Kind:           eventKindFromProto(opts.GetKind()),
			DurableGroup:   opts.GetDurableGroup(),
			Visibility:     eventVisibilityFromProto(opts.GetVisibility()),
			Description:    opts.GetDescription(),
			AckWaitSeconds: opts.GetAckWaitSeconds(),
			MaxDeliver:     opts.GetMaxDeliver(),
			GoPackage:      c.goPackage,
		}
		api.Events = append(api.Events, ev)
	}
	for _, nested := range msg.NestedType {
		nestedCtx := *c
		nestedCtx.prefix = fqn
		collectEvents(api, &nestedCtx, nested)
	}
}

// resolveEventSubject returns the explicit subject from the annotation when
// non-empty, otherwise the snake_case form of the message name. Mirrors the
// REST plugin's filename convention so REST and event subjects stay
// predictable for the same message type.
func resolveEventSubject(explicit, messageName string) string {
	if explicit != "" {
		return explicit
	}
	return camelToSnake(messageName)
}

// camelToSnake is intentionally local to avoid pulling internal/generator
// into the parser. Behaviour matches generator.ToSnakeCase for the inputs
// we expect (CamelCase message names, no digits / special chars).
func camelToSnake(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' && i > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') ||
				(prev >= 'A' && prev <= 'Z' && nextLower) {
				out = append(out, '_')
			}
		}
		if r >= 'A' && r <= 'Z' {
			out = append(out, r+32)
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

func eventKindFromProto(k optionspb.EventKind) EventKind {
	switch k {
	case optionspb.EventKind_BROADCAST:
		return EventKindBroadcast
	case optionspb.EventKind_DURABLE:
		return EventKindDurable
	case optionspb.EventKind_BOTH:
		return EventKindBoth
	default:
		return EventKindUnspecified
	}
}

func eventVisibilityFromProto(v optionspb.Visibility) EventVisibility {
	switch v {
	case optionspb.Visibility_PUBLIC:
		return EventVisibilityPublic
	case optionspb.Visibility_INTERNAL:
		return EventVisibilityInternal
	default:
		return EventVisibilityUnspecified
	}
}
