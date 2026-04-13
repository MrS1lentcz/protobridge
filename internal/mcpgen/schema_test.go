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
