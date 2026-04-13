package mcpgen

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

func TestJSONSchema_EmptyMessage(t *testing.T) {
	if got := jsonSchemaForInput(nil); got != `{"type":"object"}` {
		t.Errorf("nil: %s", got)
	}
	if got := jsonSchemaForInput(&parser.MessageType{Name: "Empty"}); got != `{"type":"object"}` {
		t.Errorf("empty fields: %s", got)
	}
}

func TestJSONSchema_ScalarsAndRequired(t *testing.T) {
	mt := &parser.MessageType{
		Name: "Req",
		Fields: []*parser.Field{
			{Name: "name", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Required: true},
			{Name: "age", Type: descriptorpb.FieldDescriptorProto_TYPE_INT32},
			{Name: "active", Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL},
		},
	}
	got := jsonSchemaForInput(mt)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	if parsed["type"] != "object" {
		t.Errorf("type: %v", parsed["type"])
	}
	props := parsed["properties"].(map[string]any)
	if props["name"].(map[string]any)["type"] != "string" {
		t.Errorf("name field: %v", props["name"])
	}
	if props["age"].(map[string]any)["format"] != "int32" {
		t.Errorf("age format: %v", props["age"])
	}
	req := parsed["required"].([]any)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("required: %v", req)
	}
}

func TestJSONSchema_RepeatedAndEnumWithAlias(t *testing.T) {
	mt := &parser.MessageType{
		Name: "Req",
		Fields: []*parser.Field{
			{Name: "tags", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Repeated: true},
			{
				Name:     "status",
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM,
				EnumValues: []*parser.EnumValue{
					{Name: "STATUS_ACTIVE", XVarName: "active"},
					{Name: "STATUS_DONE", XVarName: "done"},
				},
			},
		},
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(jsonSchemaForInput(mt)), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	props := got["properties"].(map[string]any)
	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Errorf("tags type: %v", tags)
	}
	if tags["items"].(map[string]any)["type"] != "string" {
		t.Errorf("tags items: %v", tags["items"])
	}
	status := props["status"].(map[string]any)
	enum := status["enum"].([]any)
	if len(enum) != 2 || enum[0] != "active" || enum[1] != "done" {
		t.Errorf("enum should use x_var_name aliases: %v", enum)
	}
}

func TestJSONSchema_NestedMessageStub(t *testing.T) {
	mt := &parser.MessageType{
		Name: "Outer",
		Fields: []*parser.Field{
			{Name: "page", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".x.Paging"},
		},
	}
	got := jsonSchemaForInput(mt)
	if !strings.Contains(got, `"title":"Paging"`) {
		t.Errorf("expected nested message title, got: %s", got)
	}
}

func TestJSONSchema_GoogleProtobufEmpty(t *testing.T) {
	mt := &parser.MessageType{
		Name: "Wrapper",
		Fields: []*parser.Field{
			{Name: "anything", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".google.protobuf.Empty"},
		},
	}
	got := jsonSchemaForInput(mt)
	if strings.Contains(got, `"title"`) {
		t.Errorf("Empty must not carry a title hint: %s", got)
	}
}

func TestJSONSchema_OneofIgnored(t *testing.T) {
	mt := &parser.MessageType{
		Name: "OneofMsg",
		Fields: []*parser.Field{
			{Name: "scalar", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
			{Name: "alt", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, OneofIndex: int32Ptr(0)},
		},
	}
	got := jsonSchemaForInput(mt)
	if strings.Contains(got, "alt") {
		t.Errorf("oneof field should be skipped: %s", got)
	}
}

func int32Ptr(v int32) *int32 { return &v }

func TestJSONSchema_AllScalarTypes(t *testing.T) {
	// Single test that walks every supported scalar type so the switch
	// arms in scalarOrMessageSchema are all hit at least once.
	mt := &parser.MessageType{
		Name: "AllScalars",
		Fields: []*parser.Field{
			{Name: "s", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
			{Name: "i32", Type: descriptorpb.FieldDescriptorProto_TYPE_INT32},
			{Name: "i64", Type: descriptorpb.FieldDescriptorProto_TYPE_INT64},
			{Name: "u32", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32},
			{Name: "u64", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT64},
			{Name: "f32", Type: descriptorpb.FieldDescriptorProto_TYPE_FLOAT},
			{Name: "f64", Type: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE},
			{Name: "b", Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL},
			{Name: "raw", Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES},
			{Name: "fallback", Type: descriptorpb.FieldDescriptorProto_TYPE_GROUP}, // unsupported → string fallback
		},
	}
	got := jsonSchemaForInput(mt)
	for _, tok := range []string{`"format":"int32"`, `"format":"int64"`, `"format":"uint32"`, `"format":"uint64"`, `"format":"float"`, `"format":"double"`, `"format":"byte"`, `"type":"boolean"`} {
		if !strings.Contains(got, tok) {
			t.Errorf("missing %s in: %s", tok, got)
		}
	}
}

func TestJSONSchema_EnumWithoutValues(t *testing.T) {
	// Enum with no listed values still emits a string-typed schema.
	mt := &parser.MessageType{
		Name: "M",
		Fields: []*parser.Field{
			{Name: "e", Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM},
		},
	}
	got := jsonSchemaForInput(mt)
	if !strings.Contains(got, `"type":"string"`) || strings.Contains(got, `"enum"`) {
		t.Errorf("got %s", got)
	}
}

func TestJSONSchema_RepeatedNestedMessage(t *testing.T) {
	mt := &parser.MessageType{
		Name: "M",
		Fields: []*parser.Field{
			{Name: "items", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".x.Item", Repeated: true},
		},
	}
	got := jsonSchemaForInput(mt)
	if !strings.Contains(got, `"type":"array"`) || !strings.Contains(got, `"title":"Item"`) {
		t.Errorf("got %s", got)
	}
}
