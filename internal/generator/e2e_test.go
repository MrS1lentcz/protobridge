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

	resp, err := Generate(api)
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
		"task_service.go",
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
		if f.GetName() == "task_service.go" {
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
