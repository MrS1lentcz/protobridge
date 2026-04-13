package mcpgen

import (
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// jsonSchemaForInput renders the JSON Schema for an MCP tool's input from
// the proto request message. Returned as compact JSON suitable for embedding
// into the generated handler as a string literal.
//
// Nested messages are inlined using messages (ParsedAPI.Messages) so
// consumers see the full field list instead of an empty object stub. Cycles
// in the message graph are broken via a shared seen-set that tracks which
// full names are already on the current recursion stack.
func jsonSchemaForInput(mt *parser.MessageType, messages map[string]*parser.MessageType) string {
	if mt == nil || len(mt.Fields) == 0 {
		// google.protobuf.Empty and other field-less messages → empty schema.
		return `{"type":"object"}`
	}
	schema := messageSchema(mt, map[string]bool{}, messages)
	out, err := json.Marshal(schema)
	if err != nil {
		// All values produced by messageSchema/fieldSchema are
		// json-serializable primitives, slices, and maps — a marshal
		// failure here means a future helper change introduced an
		// unmarshalable value. Fail loudly so the regression is caught
		// in tests instead of silently emitting a misleading empty schema.
		panic("mcpgen: failed to marshal generated input schema: " + err.Error())
	}
	return string(out)
}

func messageSchema(mt *parser.MessageType, seen map[string]bool, messages map[string]*parser.MessageType) map[string]any {
	if seen[mt.FullName] {
		// Cycle: emit a typed stub so the enclosing schema stays valid
		// JSON without unbounded recursion.
		stub := map[string]any{"type": "object"}
		if mt.Name != "" {
			stub["title"] = mt.Name
		}
		return stub
	}
	seenCopy := make(map[string]bool, len(seen)+1)
	for k, v := range seen {
		seenCopy[k] = v
	}
	seenCopy[mt.FullName] = true

	props := map[string]any{}
	var required []string
	for _, f := range mt.Fields {
		if f.OneofIndex != nil {
			continue // oneof variants are emitted separately if needed
		}
		props[f.Name] = fieldSchema(f, seenCopy, messages)
		if f.Required {
			required = append(required, f.Name)
		}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func fieldSchema(f *parser.Field, seen map[string]bool, messages map[string]*parser.MessageType) map[string]any {
	if f.Repeated {
		return map[string]any{
			"type":  "array",
			"items": scalarOrMessageSchema(f, seen, messages),
		}
	}
	return scalarOrMessageSchema(f, seen, messages)
}

func scalarOrMessageSchema(f *parser.Field, seen map[string]bool, messages map[string]*parser.MessageType) map[string]any {
	switch f.Type {
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return map[string]any{"type": "string"}
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		return map[string]any{"type": "integer", "format": "int32"}
	case descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return map[string]any{"type": "integer", "format": "int64"}
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		return map[string]any{"type": "integer", "format": "uint32"}
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		return map[string]any{"type": "integer", "format": "uint64"}
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return map[string]any{"type": "number", "format": "float"}
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return map[string]any{"type": "number", "format": "double"}
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return map[string]any{"type": "boolean"}
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return map[string]any{"type": "string", "format": "byte"}
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		out := map[string]any{"type": "string"}
		if len(f.EnumValues) > 0 {
			values := make([]string, 0, len(f.EnumValues))
			for _, ev := range f.EnumValues {
				name := ev.Name
				if ev.XVarName != "" {
					name = ev.XVarName
				}
				values = append(values, name)
			}
			out["enum"] = values
		}
		return out
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		// Well-known empty type: no fields to expose.
		if f.TypeName == ".google.protobuf.Empty" {
			return map[string]any{"type": "object"}
		}
		// Recurse into the fully-resolved message from the global index so
		// LLM clients see the actual field list instead of a bare
		// {"type": "object"} stub. Falls back to the stub only when the
		// referenced type is absent from the messages index (e.g. an
		// imported proto that protoc didn't include in the plugin request,
		// or a field-less message where the index holds a pointer-stable
		// stub with Fields==nil).
		if nested, ok := messages[f.TypeName]; ok && nested != nil && len(nested.Fields) > 0 {
			return messageSchema(nested, seen, messages)
		}
		short := lastSegmentOfTypeName(f.TypeName)
		stub := map[string]any{"type": "object"}
		if short != "" {
			stub["title"] = short
		}
		return stub
	default:
		return map[string]any{"type": "string"}
	}
}

func lastSegmentOfTypeName(typeName string) string {
	i := strings.LastIndex(typeName, ".")
	if i < 0 {
		return typeName
	}
	return typeName[i+1:]
}
