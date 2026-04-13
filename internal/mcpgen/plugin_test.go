package mcpgen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	parserpkg "github.com/mrs1lentcz/protobridge/internal/parser"
)

func TestParseOptions_DefaultForward(t *testing.T) {
	opts, err := ParseOptions("")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Forward) != 1 || opts.Forward[0] != "session_id" {
		t.Errorf("default forward should be [session_id], got %v", opts.Forward)
	}
}

func TestParseOptions_HandlerPkgAndForwardOverride(t *testing.T) {
	opts, err := ParseOptions("handler_pkg=example.com/h,forward=session_id;auth_token")
	if err != nil {
		t.Fatal(err)
	}
	if opts.HandlerPkg != "example.com/h" {
		t.Errorf("HandlerPkg: %q", opts.HandlerPkg)
	}
	if len(opts.Forward) != 2 || opts.Forward[0] != "session_id" || opts.Forward[1] != "auth_token" {
		t.Errorf("Forward: %v", opts.Forward)
	}
}

func TestParseOptions_UnknownKey(t *testing.T) {
	if _, err := ParseOptions("bogus=x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerate_ServiceWithMCPMethods(t *testing.T) {
	api := &parserpkg.ParsedAPI{
		Services: []*parserpkg.Service{{
			Name: "TaskService", ProtoPackage: "x.v1", GoPackage: "example.com/x/v1",
			Methods: []*parserpkg.Method{
				{
					Name: "CreateTask", StreamType: parserpkg.StreamUnary, MCP: true,
					LeadingComment: "Creates a task in the project.",
					MCPScope:       "chat session",
					InputType:      &parserpkg.MessageType{Name: "CreateTaskRequest", FullName: ".x.v1.CreateTaskRequest"},
					OutputType:     &parserpkg.MessageType{Name: "CreateTaskResponse", FullName: ".x.v1.CreateTaskResponse"},
				},
				{
					// Not MCP-enabled — must NOT be emitted as a tool.
					Name: "InternalGC", StreamType: parserpkg.StreamUnary, MCP: false,
					InputType:  &parserpkg.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
					OutputType: &parserpkg.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
				},
				{
					// Streaming — must be skipped: MCP tools are unary only.
					Name: "Watch", StreamType: parserpkg.StreamServer, MCP: true,
					InputType:  &parserpkg.MessageType{Name: "WatchReq", FullName: ".x.v1.WatchReq"},
					OutputType: &parserpkg.MessageType{Name: "Event", FullName: ".x.v1.Event"},
				},
			},
		}},
	}

	resp, err := Generate(api, Options{HandlerPkg: "example.com/x/mcp/handler", Forward: []string{"session_id"}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	files := map[string]string{}
	for _, f := range resp.File {
		files[f.GetName()] = f.GetContent()
	}
	if _, ok := files["main.go"]; !ok {
		t.Fatal("missing main.go")
	}
	handler, ok := files["handler/task_service.go"]
	if !ok {
		t.Fatalf("missing handler file; got %v", keys(files))
	}

	// Tool registration: snake_case name + description includes scope hint.
	if !strings.Contains(handler, `"create_task"`) {
		t.Errorf("expected snake_case tool name; handler:\n%s", handler)
	}
	if !strings.Contains(handler, "Available in: chat session") {
		t.Errorf("scope hint missing from description")
	}
	// Skipped methods: no tool registration for InternalGC or Watch.
	if strings.Contains(handler, `"internal_gc"`) {
		t.Error("non-MCP method must not appear as a tool")
	}
	if strings.Contains(handler, `"watch"`) {
		t.Error("streaming method must not appear as a tool")
	}

	// Both files must be parseable Go (format.Source already ran inside the
	// generator; this guards against template regressions producing valid
	// gofmt output that's still semantically broken).
	for name, content := range files {
		if _, err := parser.ParseFile(token.NewFileSet(), name, content, parser.AllErrors); err != nil {
			t.Errorf("%s not parseable Go: %v\n%s", name, err, content)
		}
	}
}

func TestGenerate_NoMCPMethodsErrors(t *testing.T) {
	api := &parserpkg.ParsedAPI{
		Services: []*parserpkg.Service{{
			Name: "X", GoPackage: "example.com/x",
			Methods: []*parserpkg.Method{{
				Name: "Get", StreamType: parserpkg.StreamUnary, MCP: false,
				InputType:  &parserpkg.MessageType{Name: "Req", FullName: ".x.Req"},
				OutputType: &parserpkg.MessageType{Name: "Resp", FullName: ".x.Resp"},
			}},
		}},
	}
	if _, err := Generate(api, Options{HandlerPkg: "example.com/h"}); err == nil {
		t.Fatal("expected error when no method is MCP-marked")
	}
}

func TestGenerate_GoogleProtobufEmptyImportsEmptypb(t *testing.T) {
	api := &parserpkg.ParsedAPI{
		Services: []*parserpkg.Service{{
			Name: "X", GoPackage: "example.com/x",
			Methods: []*parserpkg.Method{{
				Name: "Ping", StreamType: parserpkg.StreamUnary, MCP: true,
				InputType:  &parserpkg.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
				OutputType: &parserpkg.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
			}},
		}},
	}
	resp, err := Generate(api, Options{HandlerPkg: "example.com/h"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range resp.File {
		if strings.HasSuffix(f.GetName(), ".go") && strings.Contains(f.GetContent(), "emptypb.Empty") {
			if !strings.Contains(f.GetContent(), `"google.golang.org/protobuf/types/known/emptypb"`) {
				t.Errorf("file %s uses emptypb.Empty but does not import emptypb", f.GetName())
			}
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
