package generator

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

func testAPI() *parser.ParsedAPI {
	return &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "ChatService",
				ProtoPackage: "chat.v1",
				DisplayName:  "Chat",
				PathPrefix:   "/api/v1",
				Methods: []*parser.Method{
					{
						Name:       "SendMessage",
						HTTPMethod: "POST",
						HTTPPath:   "/api/v1/chats/{chat_id}/messages",
						PathParams: []string{"chat_id"},
						RequiredHeaders: []string{"X-Request-Id"},
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "SendMessageReq",
							FullName: ".chat.v1.SendMessageReq",
							Fields: []*parser.Field{
								{Name: "chat_id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Required: true},
								{Name: "text", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Required: true},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "SendMessageResp",
							FullName: ".chat.v1.SendMessageResp",
							Fields: []*parser.Field{
								{Name: "message_id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
					{
						Name:        "GetChat",
						HTTPMethod:  "GET",
						HTTPPath:    "/api/v1/chats/{chat_id}",
						PathParams:  []string{"chat_id"},
						ExcludeAuth: true,
						StreamType:  parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "GetChatReq",
							FullName: ".chat.v1.GetChatReq",
						},
						OutputType: &parser.MessageType{
							Name:     "GetChatResp",
							FullName: ".chat.v1.GetChatResp",
						},
					},
				},
			},
		},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			InputType: &parser.MessageType{
				Name:     "AuthReq",
				FullName: ".auth.v1.AuthReq",
			},
			OutputType: &parser.MessageType{
				Name:     "AuthResp",
				FullName: ".auth.v1.AuthResp",
			},
		},
	}
}

func testStreamingAPI() *parser.ParsedAPI {
	return &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "EventService",
				ProtoPackage: "events.v1",
				Methods: []*parser.Method{
					{
						Name:       "StreamEvents",
						HTTPMethod: "GET",
						HTTPPath:   "/events/stream",
						StreamType: parser.StreamServer,
						SSE:        true,
						WSMode:     "broadcast",
						InputType: &parser.MessageType{
							Name:     "StreamEventsReq",
							FullName: ".events.v1.StreamEventsReq",
							Fields: []*parser.Field{
								{Name: "topic", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "Event",
							FullName: ".events.v1.Event",
							Fields: []*parser.Field{
								{Name: "id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
								{Name: "data", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
					{
						Name:       "BidiChat",
						HTTPMethod: "GET",
						HTTPPath:   "/events/chat",
						StreamType: parser.StreamBidi,
						InputType: &parser.MessageType{
							Name:     "ChatMsg",
							FullName: ".events.v1.ChatMsg",
							Fields: []*parser.Field{
								{Name: "text", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "ChatReply",
							FullName: ".events.v1.ChatReply",
							Fields: []*parser.Field{
								{Name: "reply", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}
}

func TestGenerateServiceFile(t *testing.T) {
	api := testAPI()
	svc := api.Services[0]

	content, err := generateServiceFile(svc, api)
	if err != nil {
		t.Fatalf("generateServiceFile() error: %v", err)
	}

	checks := []string{
		"package main",
		"registerChatService",
		"func sendMessageHandler(",
		"func getChatHandler(",
		`r.Post("/api/v1/chats/{chat_id}/messages"`,
		`r.Get("/api/v1/chats/{chat_id}"`,
		"pb.SendMessageReq",
		"pb.SendMessageResp",
		"pb.GetChatReq",
		"pb.GetChatResp",
		"chi.URLParam",
		`"X-Request-Id"`,
		// ExcludeAuth on GetChat
		"getChatHandler",
		// Required fields
		`"chat_id"`,
		`"text"`,
		"ValidateRequired",
		"client.SendMessage",
		"client.GetChat",
		"QueryParamsTarget",
	}

	// ExcludeAuth: GetChat handler should NOT have auth block.
	// But SendMessage handler should have auth.
	if !strings.Contains(content, "auth(ctx, r)") {
		t.Error("SendMessage handler should contain auth call")
	}

	for _, check := range checks {
		// QueryParamsTarget is not set, so it should NOT appear
		if check == "QueryParamsTarget" {
			if strings.Contains(content, "DecodeQueryParams") {
				t.Error("should not contain DecodeQueryParams when QueryParamsTarget is empty")
			}
			continue
		}
		if !strings.Contains(content, check) {
			t.Errorf("generated service file missing %q", check)
		}
	}
}

func TestGenerateServiceFileWithQueryParamsTarget(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "ItemService",
				ProtoPackage: "items.v1",
				Methods: []*parser.Method{
					{
						Name:              "ListItems",
						HTTPMethod:        "GET",
						HTTPPath:          "/items",
						StreamType:        parser.StreamUnary,
						QueryParamsTarget: "filter",
						InputType: &parser.MessageType{
							Name:     "ListItemsReq",
							FullName: ".items.v1.ListItemsReq",
							Fields: []*parser.Field{
								{Name: "filter", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".items.v1.Filter"},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "ListItemsResp",
							FullName: ".items.v1.ListItemsResp",
						},
					},
				},
			},
		},
	}

	content, err := generateServiceFile(api.Services[0], api)
	if err != nil {
		t.Fatalf("generateServiceFile() error: %v", err)
	}
	if !strings.Contains(content, `DecodeQueryParams(r, req, "filter")`) {
		t.Error("expected DecodeQueryParams with filter target")
	}
}

func TestGenerateMain(t *testing.T) {
	api := testAPI()

	content, err := generateMain(api)
	if err != nil {
		t.Fatalf("generateMain() error: %v", err)
	}

	checks := []string{
		"package main",
		"func main()",
		"grpcx.NewPool()",
		"pool.EnableHealthWatch",
		"PROTOBRIDGE_CHAT_SERVICE_ADDR",
		"chatServiceAddr",
		"chatServiceConn",
		"registerChatService(r, chatServiceConn",
		// Auth service should be wired
		"runtime.NewAuthFunc",
		"authServiceConn",
		"PROTOBRIDGE_AUTH_SERVICE_ADDR",
		// Environment variables
		"PROTOBRIDGE_PORT",
		"PROTOBRIDGE_SENTRY_DSN",
		"PROTOBRIDGE_OTEL_ENDPOINT",
		"PROTOBRIDGE_METRICS_PORT",
		// TLS config
		"PROTOBRIDGE_TLS_CERT",
		"PROTOBRIDGE_TLS_KEY",
		// CORS
		"CORSMiddleware",
		// OTel
		"OTelMiddleware",
		// Graceful shutdown
		"signal.NotifyContext",
		"srv.Shutdown",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("generated main.go missing %q", check)
		}
	}
}

func TestGenerateMainNoAuth(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						HTTPMethod: "GET",
						HTTPPath:   "/do",
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".svc.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".svc.v1.Resp"},
					},
				},
			},
		},
	}

	content, err := generateMain(api)
	if err != nil {
		t.Fatalf("generateMain() error: %v", err)
	}
	if !strings.Contains(content, "runtime.NoAuth()") {
		t.Error("expected NoAuth() when no auth method is set")
	}
	if strings.Contains(content, "runtime.NewAuthFunc") {
		t.Error("should not contain NewAuthFunc when no auth method")
	}
}

func TestGenerateOpenAPI(t *testing.T) {
	api := testAPI()

	content := GenerateOpenAPI(api)

	checks := []string{
		"openapi: '3.1.0'",
		"info:",
		"title: protobridge API",
		"version: '1.0.0'",
		"paths:",
		"/api/v1/chats/{chat_id}/messages:",
		"post:",
		"/api/v1/chats/{chat_id}:",
		"get:",
		"operationId: ChatService_SendMessage",
		"operationId: ChatService_GetChat",
		// DisplayName used as tag
		"- Chat",
		// Path params
		"name: chat_id",
		"in: path",
		"required: true",
		// Required headers
		"name: X-Request-Id",
		"in: header",
		// Security (SendMessage has auth)
		"bearerAuth: []",
		// Request body for POST
		"requestBody:",
		"application/json:",
		"$ref: '#/components/schemas/SendMessageReq'",
		// Response
		"'200':",
		"Successful response",
		"'400':",
		"'401':",
		"'422':",
		// Schemas
		"components:",
		"schemas:",
		"SendMessageReq:",
		"type: object",
		"SendMessageResp:",
		"GetChatReq:",
		"GetChatResp:",
		// Required fields
		"required:",
		"- chat_id",
		"- text",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("OpenAPI spec missing %q", check)
		}
	}
}

func TestGenerateOpenAPIEnumWithXVarName(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						HTTPMethod: "GET",
						HTTPPath:   "/do",
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "Req",
							FullName: ".svc.v1.Req",
							Fields: []*parser.Field{
								{
									Name: "status",
									Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM,
									EnumValues: []*parser.EnumValue{
										{Name: "STATUS_ACTIVE", Number: 1, XVarName: "active"},
										{Name: "STATUS_INACTIVE", Number: 2, XVarName: "inactive"},
									},
								},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "Resp",
							FullName: ".svc.v1.Resp",
						},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)

	// x_var_name should be used instead of proto enum name
	if !strings.Contains(content, "- active") {
		t.Error("expected x_var_name 'active' in enum values")
	}
	if !strings.Contains(content, "- inactive") {
		t.Error("expected x_var_name 'inactive' in enum values")
	}
	if strings.Contains(content, "STATUS_ACTIVE") {
		t.Error("should use x_var_name instead of proto enum name")
	}
}

func TestGenerateOpenAPIFieldTypes(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						HTTPMethod: "POST",
						HTTPPath:   "/do",
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "Req",
							FullName: ".svc.v1.Req",
							Fields: []*parser.Field{
								{Name: "name", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
								{Name: "count", Type: descriptorpb.FieldDescriptorProto_TYPE_INT32},
								{Name: "big", Type: descriptorpb.FieldDescriptorProto_TYPE_INT64},
								{Name: "flag", Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL},
								{Name: "score", Type: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE},
								{Name: "data", Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES},
								{Name: "tags", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Repeated: true},
								{Name: "nested", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".svc.v1.Nested"},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "Resp",
							FullName: ".svc.v1.Resp",
						},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)

	checks := []string{
		"type: string",
		"type: integer",
		"format: int32",
		"format: int64",
		"type: boolean",
		"type: number",
		"format: double",
		"format: byte",
		"type: array",
		"$ref: '#/components/schemas/Nested'",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("OpenAPI spec missing %q", check)
		}
	}
}

func TestGenerateOpenAPISkipsStreamingMethods(t *testing.T) {
	api := testStreamingAPI()
	content := GenerateOpenAPI(api)

	// Streaming methods should not appear in OpenAPI paths.
	if strings.Contains(content, "/events/stream") {
		t.Error("streaming method should not appear in OpenAPI paths")
	}
}

func TestGenerateOpenAPIExcludeAuthNoSecurity(t *testing.T) {
	api := testAPI()
	content := GenerateOpenAPI(api)

	// GetChat has ExcludeAuth, so its section should NOT have security.
	// Find the GetChat section.
	getChatIdx := strings.Index(content, "operationId: ChatService_GetChat")
	if getChatIdx < 0 {
		t.Fatal("GetChat not found in OpenAPI spec")
	}
	// Look at next section boundary (next path or end).
	nextPathIdx := strings.Index(content[getChatIdx:], "components:")
	if nextPathIdx < 0 {
		nextPathIdx = len(content) - getChatIdx
	}
	getChatSection := content[getChatIdx : getChatIdx+nextPathIdx]
	if strings.Contains(getChatSection, "bearerAuth") {
		t.Error("GetChat (ExcludeAuth=true) should not have security in OpenAPI")
	}
}

func TestGenerateAsyncAPI(t *testing.T) {
	api := testStreamingAPI()

	content := GenerateAsyncAPI(api)
	if content == "" {
		t.Fatal("expected non-empty AsyncAPI spec for streaming methods")
	}

	checks := []string{
		"asyncapi: '3.0.0'",
		"info:",
		"title: protobridge WebSocket API",
		"version: '1.0.0'",
		"servers:",
		"protocol: ws",
		"channels:",
		"/events/stream",
		"server streaming",
		"/events/chat",
		"bidirectional streaming",
		"operations:",
		"Receive",
		"Send",
		"action: receive",
		"action: send",
		"components:",
		"messages:",
		"schemas:",
		"Event:",
		"ChatMsg:",
		"ChatReply:",
		"StreamEventsReq:",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("AsyncAPI spec missing %q", check)
		}
	}
}

func TestGenerateAsyncAPIEmptyForUnaryOnly(t *testing.T) {
	api := testAPI()
	content := GenerateAsyncAPI(api)
	if content != "" {
		t.Error("expected empty AsyncAPI spec when no streaming methods exist")
	}
}

func TestGenerateAsyncAPIBidiHasSendAndReceive(t *testing.T) {
	api := testStreamingAPI()
	content := GenerateAsyncAPI(api)

	// Bidi should have both Send and Receive operations.
	if !strings.Contains(content, "BidiChatSend:") {
		t.Error("bidi method should have Send operation")
	}
	if !strings.Contains(content, "BidiChatReceive:") {
		t.Error("bidi method should have Receive operation")
	}
}

func TestGenerateDockerfile(t *testing.T) {
	content := GenerateDockerfile()

	checks := []string{
		"FROM golang:",
		"AS build",
		"WORKDIR /app",
		"go build",
		"FROM gcr.io/distroless",
		"EXPOSE 8080",
		`ENTRYPOINT ["/protobridge"]`,
		"COPY --from=build",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("Dockerfile missing %q", check)
		}
	}
}

func TestGenerateK8sManifest(t *testing.T) {
	api := testAPI()

	content := GenerateK8sManifest(api)

	checks := []string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"name: protobridge",
		"app: protobridge",
		"replicas: 2",
		"containerPort: 8080",
		"PROTOBRIDGE_PORT",
		"PROTOBRIDGE_CHAT_SERVICE_ADDR",
		// Auth service env var (not in services list, but auth method references it)
		"PROTOBRIDGE_AUTH_SERVICE_ADDR",
		// Service resource
		"kind: Service",
		"port: 80",
		"targetPort: 8080",
		"ClusterIP",
		// Health checks
		"readinessProbe:",
		"livenessProbe:",
		"/health",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("K8s manifest missing %q", check)
		}
	}
}

func TestGenerateK8sManifestAuthServiceInServices(t *testing.T) {
	// When auth service is also in the services list, no duplicate env var.
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "AuthService",
				ProtoPackage: "auth.v1",
				Methods: []*parser.Method{
					{
						Name:       "Login",
						HTTPMethod: "POST",
						HTTPPath:   "/login",
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "LoginReq", FullName: ".auth.v1.LoginReq"},
						OutputType: &parser.MessageType{Name: "LoginResp", FullName: ".auth.v1.LoginResp"},
					},
				},
			},
		},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
		},
	}

	content := GenerateK8sManifest(api)
	count := strings.Count(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR")
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of PROTOBRIDGE_AUTH_SERVICE_ADDR, got %d", count)
	}
}

func TestGenerateEnvExample(t *testing.T) {
	api := testAPI()

	content := GenerateEnvExample(api)

	checks := []string{
		"protobridge",
		"PROTOBRIDGE_CHAT_SERVICE_ADDR=localhost:50051",
		"PROTOBRIDGE_AUTH_SERVICE_ADDR=localhost:50051",
		"PROTOBRIDGE_PORT=8080",
		"PROTOBRIDGE_TLS_CERT",
		"PROTOBRIDGE_TLS_KEY",
		"PROTOBRIDGE_TLS_SERVER_NAME",
		"PROTOBRIDGE_CHAT_SERVICE_TLS",
		"PROTOBRIDGE_CORS_ORIGINS",
		"PROTOBRIDGE_CORS_METHODS",
		"PROTOBRIDGE_CORS_HEADERS",
		"PROTOBRIDGE_CORS_MAX_AGE",
		"PROTOBRIDGE_SENTRY_DSN",
		"PROTOBRIDGE_OTEL_ENDPOINT",
		"PROTOBRIDGE_OTEL_SERVICE_NAME",
		"PROTOBRIDGE_METRICS_PORT",
		"PROTOBRIDGE_GRPC_OPTIONS",
		"PROTOBRIDGE_CHAT_SERVICE_GRPC_OPTIONS",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf(".env.example missing %q", check)
		}
	}
}

func TestGenerateEnvDefaults(t *testing.T) {
	api := testAPI()

	content := GenerateEnvDefaults(api)

	checks := []string{
		"PROTOBRIDGE_PORT=8080",
		"PROTOBRIDGE_OTEL_SERVICE_NAME=protobridge",
		"PROTOBRIDGE_CORS_ORIGINS=*",
		"PROTOBRIDGE_CORS_METHODS=GET,POST,PUT,DELETE,PATCH,OPTIONS",
		"PROTOBRIDGE_CORS_HEADERS=Content-Type,Authorization",
		"PROTOBRIDGE_CORS_MAX_AGE=86400",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf(".env.defaults missing %q", check)
		}
	}
}

func TestGenerateWSHandler(t *testing.T) {
	svc := &parser.Service{
		Name:         "EventService",
		ProtoPackage: "events.v1",
	}
	m := &parser.Method{
		Name:       "StreamEvents",
		StreamType: parser.StreamBidi,
		InputType: &parser.MessageType{
			Name:     "StreamEventsReq",
			FullName: ".events.v1.StreamEventsReq",
		},
		OutputType: &parser.MessageType{
			Name:     "Event",
			FullName: ".events.v1.Event",
		},
	}

	content, err := generateWSHandler(svc, m)
	if err != nil {
		t.Fatalf("generateWSHandler() error: %v", err)
	}

	checks := []string{
		"streamEventsWSHandler",
		"streamEventsStreamFactory",
		"streamEventsStreamProxy",
		"EventServiceClient",
		"StreamEvents(ctx)",
		"StreamEventsReq",
		"OpenStream",
		"Send(msg",
		"Recv()",
		"CloseSend()",
		"NewRequestMessage()",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("WS handler missing %q", check)
		}
	}
}

func TestGenerateFullPipeline(t *testing.T) {
	api := testAPI()

	resp, err := Generate(api)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Expected files: service file + main.go + openapi.yaml + Dockerfile + k8s.yaml + .env.example + .env.defaults
	// No asyncapi.yaml because testAPI has no streaming methods.
	fileNames := make(map[string]bool)
	for _, f := range resp.File {
		fileNames[f.GetName()] = true
	}

	expected := []string{
		"chat_service.go",
		"main.go",
		"schema/openapi.yaml",
		"Dockerfile",
		"k8s.yaml",
		".env.example",
		".env.defaults",
	}

	for _, name := range expected {
		if !fileNames[name] {
			t.Errorf("missing generated file: %s", name)
		}
	}

	// asyncapi.yaml should NOT be present (no streaming).
	if fileNames["schema/asyncapi.yaml"] {
		t.Error("asyncapi.yaml should not be generated when there are no streaming methods")
	}
}

func TestGenerateFullPipelineWithStreaming(t *testing.T) {
	api := testStreamingAPI()

	resp, err := Generate(api)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	fileNames := make(map[string]bool)
	for _, f := range resp.File {
		fileNames[f.GetName()] = true
	}

	if !fileNames["schema/asyncapi.yaml"] {
		t.Error("asyncapi.yaml should be generated when streaming methods exist")
	}
}

func TestGenerateOpenAPIRepeatedField(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						HTTPMethod: "POST",
						HTTPPath:   "/do",
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "Req",
							FullName: ".svc.v1.Req",
							Fields: []*parser.Field{
								{Name: "ids", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Repeated: true},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "Resp",
							FullName: ".svc.v1.Resp",
						},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)
	if !strings.Contains(content, "type: array") {
		t.Error("repeated field should generate array type in OpenAPI")
	}
}

func TestGenerateOpenAPIOneofComment(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						HTTPMethod: "POST",
						HTTPPath:   "/do",
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "Req",
							FullName: ".svc.v1.Req",
							Fields: []*parser.Field{
								{Name: "text", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, OneofIndex: new(int32)},
							},
							OneofDecls: []*parser.OneofDecl{
								{Name: "payload", Variants: []*parser.OneofVariant{{FieldName: "text"}}},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "Resp",
							FullName: ".svc.v1.Resp",
						},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)
	if !strings.Contains(content, "# oneof: payload") {
		t.Error("expected oneof comment in OpenAPI schema")
	}
}

func TestChiMethodName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"GET", "Get"},
		{"POST", "Post"},
		{"PUT", "Put"},
		{"DELETE", "Delete"},
		{"PATCH", "Patch"},
		{"UNKNOWN", "HandleFunc"},
	}
	for _, tt := range tests {
		got := chiMethodName(tt.in)
		if got != tt.want {
			t.Errorf("chiMethodName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGuessProtoImport(t *testing.T) {
	svc := &parser.Service{ProtoPackage: "chat.v1"}
	got := guessProtoImport(svc)
	if got != "chat/v1" {
		t.Errorf("guessProtoImport() = %q, want chat/v1", got)
	}
}

func TestStreamLabel(t *testing.T) {
	tests := []struct {
		st   parser.StreamType
		want string
	}{
		{parser.StreamUnary, "unary"},
		{parser.StreamServer, "server streaming"},
		{parser.StreamClient, "client streaming"},
		{parser.StreamBidi, "bidirectional streaming"},
	}
	for _, tt := range tests {
		got := streamLabel(tt.st)
		if got != tt.want {
			t.Errorf("streamLabel(%v) = %q, want %q", tt.st, got, tt.want)
		}
	}
}
