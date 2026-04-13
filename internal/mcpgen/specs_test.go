package mcpgen

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

func sampleAPI() *parser.ParsedAPI {
	return &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "TaskService",
			Methods: []*parser.Method{
				{
					Name: "CreateTask", StreamType: parser.StreamUnary, MCP: true,
					LeadingComment: "Creates a new task on the board.\nReturns the created task with its ID.",
					MCPScope:       "chat session",
					InputType: &parser.MessageType{
						Name: "CreateTaskRequest", FullName: ".x.CreateTaskRequest",
						Fields: []*parser.Field{
							{Name: "title", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Required: true},
							{Name: "description", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
						},
					},
					OutputType: &parser.MessageType{
						Name: "Task", FullName: ".x.Task",
						Fields: []*parser.Field{
							{Name: "id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
						},
					},
				},
				// Methods that must NOT appear in either schema:
				{Name: "InternalGC", StreamType: parser.StreamUnary, MCP: false,
					InputType: &parser.MessageType{Name: "X"}, OutputType: &parser.MessageType{Name: "X"}},
				{Name: "Watch", StreamType: parser.StreamServer, MCP: true,
					InputType: &parser.MessageType{Name: "X"}, OutputType: &parser.MessageType{Name: "X"}},
				// Method with no input fields → empty schema, no params.
				{Name: "Ping", StreamType: parser.StreamUnary, MCP: true,
					LeadingComment: "Health check.",
					InputType:      &parser.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
					OutputType:     &parser.MessageType{Name: "PingResponse", FullName: ".x.PingResponse"}},
			},
		}},
	}
}

func TestGenerateOpenRPC_Shape(t *testing.T) {
	got := generateOpenRPC(sampleAPI())
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	if doc["openrpc"] != "1.3.2" {
		t.Errorf("openrpc version: %v", doc["openrpc"])
	}
	if _, ok := doc["info"].(map[string]any); !ok {
		t.Error("info missing")
	}
	methods := doc["methods"].([]any)
	if len(methods) != 2 {
		t.Fatalf("expected 2 methods (create_task + ping), got %d", len(methods))
	}
	// Alphabetical order — create_task before ping.
	names := []string{methods[0].(map[string]any)["name"].(string), methods[1].(map[string]any)["name"].(string)}
	if names[0] != "create_task" || names[1] != "ping" {
		t.Errorf("methods not sorted: %v", names)
	}

	createTask := methods[0].(map[string]any)
	if got := createTask["summary"].(string); got != "Creates a new task on the board." {
		t.Errorf("summary should be first line of leading comment, got %q", got)
	}
	if !strings.Contains(createTask["description"].(string), "Available in: chat session") {
		t.Errorf("description should include scope hint: %v", createTask["description"])
	}

	params := createTask["params"].([]any)
	if len(params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(params))
	}
	first := params[0].(map[string]any)
	if first["name"] != "title" || first["required"] != true {
		t.Errorf("title param missing or not required: %v", first)
	}

	result := createTask["result"].(map[string]any)
	if result["name"] != "result" {
		t.Errorf("result name: %v", result["name"])
	}

	// Streaming method must not appear.
	for _, m := range methods {
		if name := m.(map[string]any)["name"].(string); name == "watch" || name == "internal_gc" {
			t.Errorf("non-MCP/streaming method leaked into OpenRPC: %s", name)
		}
	}
}

func TestGenerateOpenRPC_PingHasNoParams(t *testing.T) {
	got := generateOpenRPC(sampleAPI())
	var doc map[string]any
	_ = json.Unmarshal([]byte(got), &doc)
	for _, m := range doc["methods"].([]any) {
		if m.(map[string]any)["name"] != "ping" {
			continue
		}
		if params, ok := m.(map[string]any)["params"]; ok && params != nil {
			if arr, ok := params.([]any); ok && len(arr) > 0 {
				t.Errorf("ping should have no params (Empty input), got %v", arr)
			}
		}
	}
}

func TestGenerateMCPTools_Shape(t *testing.T) {
	got := generateMCPTools(sampleAPI())
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	tools := doc["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "create_task" {
		t.Errorf("first tool: %v", first["name"])
	}
	if _, ok := first["inputSchema"].(map[string]any); !ok {
		t.Errorf("inputSchema must be an object: %v", first["inputSchema"])
	}
	if !strings.Contains(first["description"].(string), "Available in: chat session") {
		t.Errorf("description: %v", first["description"])
	}
}

func TestGenerateMCPTools_EmptyAPI(t *testing.T) {
	// No services / no MCP methods → empty tools list, still valid JSON.
	got := generateMCPTools(&parser.ParsedAPI{})
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	if tools, ok := doc["tools"].([]any); ok && len(tools) > 0 {
		t.Errorf("expected empty tools, got %v", tools)
	}
}

func TestGenerateOpenRPC_EmptyAPI(t *testing.T) {
	got := generateOpenRPC(&parser.ParsedAPI{})
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	if methods, ok := doc["methods"].([]any); ok && len(methods) > 0 {
		t.Errorf("expected empty methods, got %v", methods)
	}
}

func TestSummaryLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"single line", "single line"},
		{"first\nsecond\nthird", "first"},
	}
	for _, tc := range cases {
		if got := summaryLine(tc.in); got != tc.want {
			t.Errorf("summaryLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParamsFromMessage_OneofSkipped(t *testing.T) {
	mt := &parser.MessageType{
		Name: "M",
		Fields: []*parser.Field{
			{Name: "scalar", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
			{Name: "alt", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, OneofIndex: int32Ptr(0)},
		},
	}
	params := paramsFromMessage(mt)
	if len(params) != 1 || params[0].Name != "scalar" {
		t.Errorf("oneof field should be skipped: %v", params)
	}
}
