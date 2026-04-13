package mcpgen

import (
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// messageSchema's self-reference guard now fires in production whenever a
// proto message contains a field referencing itself (or a mutually
// recursive peer): scalarOrMessageSchema recurses into nested types found
// in the messages index, and this seen-set check breaks the cycle by
// emitting a typed stub. The internal test covers the guard directly so a
// regression that drops the cycle check is caught even for single-message
// inputs where a full recursive fixture would be overkill.
func TestMessageSchema_SelfReferenceReturnsStub(t *testing.T) {
	mt := &parser.MessageType{
		Name:     "Node",
		FullName: ".x.Node",
		Fields: []*parser.Field{
			{Name: "v", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
		},
	}
	seen := map[string]bool{".x.Node": true}
	got := messageSchema(mt, seen, nil)
	if got["type"] != "object" {
		t.Errorf("self-ref guard should return generic object stub, got %v", got)
	}
	if _, hasProps := got["properties"]; hasProps {
		t.Errorf("self-ref stub must not enumerate properties, got %v", got)
	}
}

func TestMessageSchema_CopiesSeenSet(t *testing.T) {
	// The function copies the caller's seen map before adding its own
	// entry — verify mutation isolation so concurrent recursive calls
	// can't trample each other.
	mt := &parser.MessageType{
		Name: "M", FullName: ".x.M",
		Fields: []*parser.Field{
			{Name: "v", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
		},
	}
	caller := map[string]bool{".x.Other": true}
	_ = messageSchema(mt, caller, nil)
	if caller[".x.M"] {
		t.Error("messageSchema mutated the caller's seen map")
	}
	if _, ok := caller[".x.Other"]; !ok {
		t.Error("messageSchema removed pre-existing entry from caller's seen map")
	}
}
