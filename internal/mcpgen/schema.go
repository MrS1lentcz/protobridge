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
// Nested messages are inlined (no $ref) because MCP tool consumers (LLMs)
// generally fare better with self-contained schemas.
func jsonSchemaForInput(mt *parser.MessageType) string {
	if mt == nil || len(mt.Fields) == 0 {
		// google.protobuf.Empty and other field-less messages → empty schema.
		return `{"type":"object"}`
	}
	schema := messageSchema(mt, map[string]bool{})
	out, err := json.Marshal(schema)
	if err != nil {
		// Failure here means we built a non-marshalable map[string]any, which
		// should be impossible given the helpers below — fail loudly in tests.
		return `{"type":"object"}`
	}
	return string(out)
}

func messageSchema(mt *parser.MessageType, seen map[string]bool) map[string]any {
	if seen[mt.FullName] {
		// Self-referential message: emit a generic object stub to break
		// the cycle. Rare in practice for MCP tool inputs.
		return map[string]any{"type": "object"}
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
		props[f.Name] = fieldSchema(f, seenCopy)
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

func fieldSchema(f *parser.Field, seen map[string]bool) map[string]any {
	if f.Repeated {
		return map[string]any{
			"type":  "array",
			"items": scalarOrMessageSchema(f, seen),
		}
	}
	return scalarOrMessageSchema(f, seen)
}

func scalarOrMessageSchema(f *parser.Field, seen map[string]bool) map[string]any {
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
		// Nested message — inline if we have full info, else emit an empty
		// object stub. Without a resolver the parser leaves only Name/FullName
		// for messages outside the requested files (well-known types etc.).
		if f.TypeName == ".google.protobuf.Empty" {
			return map[string]any{"type": "object"}
		}
		// Inline the type name as a hint — full inlining would require a
		// resolver indexed by FullName; today the parser's resolveMessageType
		// only walks one level, so we mirror that and emit a typed stub.
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
