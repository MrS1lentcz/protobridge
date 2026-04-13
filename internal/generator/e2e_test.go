package generator

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	internalparser "github.com/mrs1lentcz/protobridge/internal/parser"
)

// TestE2E_GeneratedCodeIsSyntacticallyValid generates code from a realistic
// ParsedAPI (matching the taskboard example) and verifies that every
// generated .go file is syntactically valid Go.
func TestE2E_GeneratedCodeIsSyntacticallyValid(t *testing.T) {
	api := &internalparser.ParsedAPI{
		Services: []*internalparser.Service{
			{
				Name:         "TaskService",
				ProtoPackage: "taskboard.v1",
				GoPackage:    "github.com/mrs1lentcz/protobridge/example/taskboard/gen/grpc/taskboard/v1",
				DisplayName:  "Tasks",
				PathPrefix:   "/api/v1",
				Methods: []*internalparser.Method{
					{
						Name:       "CreateTask",
						InputType:  &internalparser.MessageType{Name: "CreateTaskRequest", FullName: ".taskboard.v1.CreateTaskRequest", Fields: []*internalparser.Field{{Name: "title", Required: true}}},
						OutputType: &internalparser.MessageType{Name: "Task", FullName: ".taskboard.v1.Task"},
						HTTPMethod: "POST",
						HTTPPath:   "/api/v1/tasks",
						RequiredHeaders: []string{"user_id"},
						StreamType: internalparser.StreamUnary,
					},
					{
						Name:       "GetTask",
						InputType:  &internalparser.MessageType{Name: "GetTaskRequest", FullName: ".taskboard.v1.GetTaskRequest"},
						OutputType: &internalparser.MessageType{Name: "Task", FullName: ".taskboard.v1.Task"},
						HTTPMethod: "GET",
						HTTPPath:   "/api/v1/tasks/{task_id}",
						PathParams: []string{"task_id"},
						StreamType: internalparser.StreamUnary,
					},
					{
						Name:       "WatchTasks",
						InputType:  &internalparser.MessageType{Name: "WatchTasksRequest", FullName: ".taskboard.v1.WatchTasksRequest"},
						OutputType: &internalparser.MessageType{Name: "TaskEvent", FullName: ".taskboard.v1.TaskEvent"},
						HTTPMethod: "GET",
						HTTPPath:   "/api/v1/tasks/watch",
						StreamType: internalparser.StreamServer,
						WSMode:     "private",
					},
					{
						Name:       "TaskNotifications",
						InputType:  &internalparser.MessageType{Name: "WatchTasksRequest", FullName: ".taskboard.v1.WatchTasksRequest"},
						OutputType: &internalparser.MessageType{Name: "TaskEvent", FullName: ".taskboard.v1.TaskEvent"},
						HTTPMethod: "GET",
						HTTPPath:   "/api/v1/tasks/notifications",
						StreamType: internalparser.StreamServer,
						SSE:        true,
					},
					{
						Name:       "BulkCreateTasks",
						InputType:  &internalparser.MessageType{Name: "BulkCreateTaskRequest", FullName: ".taskboard.v1.BulkCreateTaskRequest"},
						OutputType: &internalparser.MessageType{Name: "BulkCreateTaskResponse", FullName: ".taskboard.v1.BulkCreateTaskResponse"},
						HTTPMethod: "POST",
						HTTPPath:   "/api/v1/tasks/bulk",
						StreamType: internalparser.StreamClient,
					},
					{
						Name:       "TaskChat",
						InputType:  &internalparser.MessageType{Name: "ChatMessage", FullName: ".taskboard.v1.ChatMessage"},
						OutputType: &internalparser.MessageType{Name: "ChatMessage", FullName: ".taskboard.v1.ChatMessage"},
						HTTPMethod: "GET",
						HTTPPath:   "/api/v1/tasks/{task_id}/chat",
						PathParams: []string{"task_id"},
						StreamType: internalparser.StreamBidi,
					},
				},
			},
		},
		AuthMethod: &internalparser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			GoPackage:   "github.com/mrs1lentcz/protobridge/example/taskboard/gen/grpc/taskboard/v1",
			InputType:   &internalparser.MessageType{Name: "AuthRequest", FullName: ".taskboard.v1.AuthRequest"},
			OutputType:  &internalparser.MessageType{Name: "AuthResponse", FullName: ".taskboard.v1.AuthResponse"},
		},
	}

	resp, err := Generate(api, Options{HandlerPkg: "example.com/test/handler"})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	fset := token.NewFileSet()
	goFiles := 0

	for _, f := range resp.File {
		name := f.GetName()
		content := f.GetContent()

		if !strings.HasSuffix(name, ".go") {
			continue
		}
		goFiles++

		// Parse as Go source – catches syntax errors.
		_, err := parser.ParseFile(fset, name, content, parser.AllErrors)
		if err != nil {
			t.Errorf("generated file %s has syntax errors:\n%v\n\nContent:\n%s", name, err, content)
		}
	}

	if goFiles == 0 {
		t.Fatal("no .go files generated")
	}

	// Verify expected files exist.
	fileNames := make(map[string]bool)
	for _, f := range resp.File {
		fileNames[f.GetName()] = true
	}

	expectedFiles := []string{
		"handler/task_service.go",
		"main.go",
		"schema/openapi.yaml",
		"schema/asyncapi.yaml",
		"Dockerfile",
		"k8s.yaml",
		".env.example",
		".env.defaults",
	}
	for _, name := range expectedFiles {
		if !fileNames[name] {
			t.Errorf("missing expected file: %s", name)
		}
	}

	// Verify main.go contains auth wiring with correct import path.
	mainContent := ""
	for _, f := range resp.File {
		if f.GetName() == "main.go" {
			mainContent = f.GetContent()
			break
		}
	}

	mainChecks := []string{
		"github.com/mrs1lentcz/protobridge/example/taskboard/gen/grpc/taskboard/v1",
		"Authenticate",
		"AuthRequest",
		"ConnectScaled",
		"ScalingConfig",
		"/healthz",
		"proto.Marshal",
	}
	for _, check := range mainChecks {
		if !strings.Contains(mainContent, check) {
			t.Errorf("main.go missing expected content: %q", check)
		}
	}

	// Verify service file contains all handler types.
	svcContent := ""
	for _, f := range resp.File {
		if f.GetName() == "handler/task_service.go" {
			svcContent = f.GetContent()
			break
		}
	}

	svcChecks := []string{
		"createTaskHandler",     // unary
		"watchTasksHandler",     // server stream (WS)
		"taskNotificationsHandler", // SSE
		"bulkCreateTasksHandler",   // client stream
		"taskChatHandler",          // bidi stream
		"websocket.Accept",         // WS upgrade
		"text/event-stream",        // SSE
		"stream.CloseSend",         // bidi
		"stream.CloseAndRecv",      // client stream
	}
	for _, check := range svcChecks {
		if !strings.Contains(svcContent, check) {
			t.Errorf("task_service.go missing expected content: %q", check)
		}
	}
}

func TestE2E_StreamingHandlersHaveErrorChecks(t *testing.T) {
	// Regression: generated code for streaming handlers must check protojson.Marshal
	// errors instead of using `data, _ := protojson.Marshal(msg)`.
	api := &internalparser.ParsedAPI{
		Services: []*internalparser.Service{
			{
				Name:         "EventService",
				ProtoPackage: "events.v1",
				GoPackage:    "github.com/example/events/v1",
				Methods: []*internalparser.Method{
					{
						Name:       "StreamEvents",
						InputType:  &internalparser.MessageType{Name: "StreamReq", FullName: ".events.v1.StreamReq"},
						OutputType: &internalparser.MessageType{Name: "Event", FullName: ".events.v1.Event"},
						HTTPMethod: "GET",
						HTTPPath:   "/events/stream",
						StreamType: internalparser.StreamServer,
						SSE:        true,
					},
					{
						Name:       "WatchEvents",
						InputType:  &internalparser.MessageType{Name: "WatchReq", FullName: ".events.v1.WatchReq"},
						OutputType: &internalparser.MessageType{Name: "Event", FullName: ".events.v1.Event"},
						HTTPMethod: "GET",
						HTTPPath:   "/events/watch",
						StreamType: internalparser.StreamServer,
						WSMode:     "broadcast",
					},
					{
						Name:       "Chat",
						InputType:  &internalparser.MessageType{Name: "ChatMsg", FullName: ".events.v1.ChatMsg"},
						OutputType: &internalparser.MessageType{Name: "ChatReply", FullName: ".events.v1.ChatReply"},
						HTTPMethod: "GET",
						HTTPPath:   "/events/chat",
						StreamType: internalparser.StreamBidi,
					},
				},
			},
		},
	}

	resp, err := Generate(api, Options{HandlerPkg: "example.com/test/handler"})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Find the service file.
	var svcContent string
	for _, f := range resp.File {
		if f.GetName() == "handler/event_service.go" {
			svcContent = f.GetContent()
			break
		}
	}
	if svcContent == "" {
		t.Fatal("handler/event_service.go not generated")
	}

	// The generated code must NOT use `data, _ :=` pattern for protojson.Marshal.
	if strings.Contains(svcContent, "data, _ :=") {
		t.Error("generated streaming handler uses 'data, _ :=' which ignores marshal errors")
	}

	// The generated code must check the error from protojson.Marshal.
	// Look for the pattern where data is used with an error check.
	marshalCount := strings.Count(svcContent, "protojson.Marshal(")
	errCheckCount := strings.Count(svcContent, "data, err := protojson.Marshal(") +
		strings.Count(svcContent, "result, err := protojson.Marshal(")
	if marshalCount > 0 && errCheckCount == 0 {
		t.Error("generated code calls protojson.Marshal but never checks the error")
	}
}
