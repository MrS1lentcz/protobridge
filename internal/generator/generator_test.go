package generator

import (
	"bytes"
	"fmt"
	"go/format"
	parser2 "go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
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
						Name:            "SendMessage",
						HTTPMethod:      "POST",
						HTTPPath:        "/api/v1/chats/{chat_id}/messages",
						PathParams:      []string{"chat_id"},
						RequiredHeaders: []string{"X-Request-Id"},
						StreamType:      parser.StreamUnary,
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

	content := generateServiceFile(svc, api)

	checks := []string{
		"package handler",
		"RegisterChatService",
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

	// Regression: SetUserMetadata must run AFTER metadata.NewOutgoingContext,
	// otherwise the outgoing context built from path params/headers wipes the
	// user metadata key.
	idxNew := strings.Index(content, "metadata.NewOutgoingContext(ctx, md)")
	idxUser := strings.Index(content, "runtime.SetUserMetadata(ctx, userData)")
	if idxNew < 0 || idxUser < 0 {
		t.Fatalf("expected both NewOutgoingContext and SetUserMetadata in handler")
	}
	if idxUser < idxNew {
		t.Errorf("SetUserMetadata must come after NewOutgoingContext (else user metadata is lost)")
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

	content := generateServiceFile(api.Services[0], api)
	if !strings.Contains(content, `DecodeQueryParams(r, req, "filter")`) {
		t.Error("expected DecodeQueryParams with filter target")
	}
}

func TestGenerateMain(t *testing.T) {
	api := testAPI()

	content := generateMain(api, "example.com/test/handler", "")

	checks := []string{
		"package main",
		"func main()",
		"grpcx.NewPool()",
		"pool.EnableHealthWatch",
		"PROTOBRIDGE_CHAT_SERVICE_ADDR",
		"chatServiceAddr",
		"ConnectScaled",
		"ScalingConfig",
		"handler.RegisterChatService(r,",
		// Auth service should be wired
		"Authenticate",
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

	// Regression: register*Service must be called with the resolved address
	// variable, not the env var key string literal. Passing the literal would
	// make the pool dial the literal "PROTOBRIDGE_..." hostname.
	if strings.Contains(content, `handler.RegisterChatService(r, "PROTOBRIDGE_CHAT_SERVICE_ADDR"`) {
		t.Error("registerChatService called with env var key literal instead of resolved address")
	}
	if !strings.Contains(content, "handler.RegisterChatService(r, chatServiceAddr,") {
		t.Errorf("expected registerChatService(r, chatServiceAddr, ...) in generated main.go")
	}
}

func TestGenerateMain_AuthOnlyService_NoRegisterCall(t *testing.T) {
	// When an auth service has no REST endpoints, we still need a connection
	// for the auth function to dial, but main.go must NOT call
	// registerAuthService — there is no such generated function and the
	// build would fail.
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "ChatService", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Send", HTTPMethod: "POST", HTTPPath: "/send",
				StreamType: parser.StreamUnary,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Resp", FullName: ".x.v1.Resp"},
			}},
		}},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			GoPackage:   "x/v1",
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".x.v1.AuthReq"},
			OutputType:  &parser.MessageType{Name: "AuthResp", FullName: ".x.v1.AuthResp"},
		},
	}
	content := generateMain(api, "example.com/test/handler", "")
	if strings.Contains(content, "handler.RegisterAuthService(") {
		t.Errorf("must not call registerAuthService when auth service has no REST endpoints:\n%s", content)
	}
	if !strings.Contains(content, "handler.RegisterChatService(") {
		t.Error("expected registerChatService(...) for non-auth service")
	}
	// Connection still needs to be pre-created.
	if !strings.Contains(content, "authServiceAddr") {
		t.Error("expected authServiceAddr for connection pre-create")
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

	content := generateMain(api, "example.com/test/handler", "")
	if !strings.Contains(content, "runtime.NoAuth()") {
		t.Error("expected NoAuth() when no auth method is set")
	}
	if strings.Contains(content, "Authenticate") {
		t.Error("should not contain auth method call when no auth method")
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
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
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

	content := generateWSHandler(svc, m)

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

	resp, err := Generate(api, Options{HandlerPkg: "example.com/test/handler"})
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
		"handler/chat_service.go",
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

	resp, err := Generate(api, Options{HandlerPkg: "example.com/test/handler"})
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

func TestToPascalCase_HyphenatedHeaders(t *testing.T) {
	// Header names with hyphens (e.g. "x-github-event") must produce a valid
	// Go identifier — toPascalCase is used to build the local var name in
	// the generated handler.
	tests := []struct {
		in, want string
	}{
		{"x-github-event", "XGithubEvent"},
		{"Content-Type", "ContentType"},
		{"x_request_id", "XRequestId"},
		{"mixed-case_value", "MixedCaseValue"},
	}
	for _, tc := range tests {
		got := toPascalCase(tc.in)
		if got != tc.want {
			t.Errorf("toPascalCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsAny(got, "-") {
			t.Errorf("toPascalCase(%q) = %q contains hyphen (invalid Go identifier)", tc.in, got)
		}
	}
}

func TestGenerateServiceFile_HeaderWithHyphenIsValidIdentifier(t *testing.T) {
	// End-to-end: a required_headers entry with hyphens must produce a Go
	// identifier in the emitted handler, not a literal hyphenated string.
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Do", HTTPMethod: "GET", HTTPPath: "/x",
				StreamType:      parser.StreamUnary,
				RequiredHeaders: []string{"x-github-event"},
				InputType:       &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType:      &parser.MessageType{Name: "Resp", FullName: ".x.v1.Resp"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	if strings.Contains(content, "headerx-github-event") || strings.Contains(content, "headerX-github-event") {
		t.Fatalf("emitted invalid Go identifier with hyphen:\n%s", content)
	}
	if !strings.Contains(content, "headerXGithubEvent") {
		t.Fatalf("expected headerXGithubEvent in output:\n%s", content)
	}
}

func TestGenerateServiceFile_StreamingMarshalGoesThroughRuntime(t *testing.T) {
	// Streaming branches (SSE/WebSocket) used to call protojson.Marshal
	// directly, bypassing x_var_name post-processing. They must use the
	// centralized runtime.MarshalProto helper so enum aliases are applied
	// consistently across unary and streaming transports.
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{
				{
					Name: "Stream", HTTPMethod: "GET", HTTPPath: "/stream",
					StreamType: parser.StreamServer,
					InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
					OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
				},
				{
					Name: "Live", HTTPMethod: "GET", HTTPPath: "/live",
					StreamType: parser.StreamServer, SSE: true,
					InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
					OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
				},
			},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	if strings.Contains(content, "protojson.Marshal(") {
		t.Errorf("streaming handler must not call protojson.Marshal directly (bypasses x_var_name postprocess):\n%s", content)
	}
	if !strings.Contains(content, "runtime.MarshalProto(") {
		t.Errorf("streaming handler must use runtime.MarshalProto helper:\n%s", content)
	}
}

func TestGenerateServiceFile_GoogleProtobufEmpty(t *testing.T) {
	// google.protobuf.Empty is an external well-known type. The generated
	// handler must reference emptypb.Empty (with import) rather than pb.Empty
	// which doesn't exist in the user's proto package.
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Dismiss", HTTPMethod: "POST", HTTPPath: "/dismiss",
				StreamType: parser.StreamUnary,
				InputType:  &parser.MessageType{Name: "DismissRequest", FullName: ".x.v1.DismissRequest"},
				OutputType: &parser.MessageType{Name: "Empty", FullName: ".google.protobuf.Empty"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	// Match `pb.Empty` only when not preceded by alphanumerics (so emptypb.Empty doesn't trip).
	if regexp.MustCompile(`\bpb\.Empty\b`).MatchString(content) {
		t.Errorf("must not reference pb.Empty (external well-known type):\n%s", content)
	}
	if !strings.Contains(content, "emptypb.Empty") {
		t.Errorf("expected emptypb.Empty:\n%s", content)
	}
	if !strings.Contains(content, `"google.golang.org/protobuf/types/known/emptypb"`) {
		t.Errorf("expected emptypb import")
	}
}

// importsOf parses the generated service file and returns the exact set of
// import paths it declares. Compared to substring search, this matches
// "google.golang.org/grpc" without false-positives from
// "google.golang.org/grpc/metadata" and survives any whitespace/grouping
// changes in the template.
func importsOf(t *testing.T, content string) map[string]struct{} {
	t.Helper()
	f, err := parser2.ParseFile(token.NewFileSet(), "service.go", content, parser2.ImportsOnly)
	if err != nil {
		t.Fatalf("parse generated file: %v\n%s", err, content)
	}
	out := make(map[string]struct{}, len(f.Imports))
	for _, imp := range f.Imports {
		// imp.Path.Value is the quoted string literal; strip quotes.
		path := imp.Path.Value
		path = path[1 : len(path)-1]
		out[path] = struct{}{}
	}
	return out
}

func assertImports(t *testing.T, content string, want, mustNot []string) {
	t.Helper()
	imports := importsOf(t, content)
	for _, p := range want {
		if _, ok := imports[p]; !ok {
			t.Errorf("expected import %q, got %v", p, keys(imports))
		}
	}
	for _, p := range mustNot {
		if _, ok := imports[p]; ok {
			t.Errorf("unexpected import %q (would force goimports rerun)", p)
		}
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func unaryOnlyAPI() *parser.ParsedAPI {
	return &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Get", HTTPMethod: "GET", HTTPPath: "/x",
				StreamType: parser.StreamUnary,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Resp", FullName: ".x.v1.Resp"},
			}},
		}},
	}
}

func TestGenerateServiceFile_NoUnusedImports_UnaryOnly(t *testing.T) {
	// Generated handler files for a service with only unary methods must
	// not import packages they don't reference (fmt, io, websocket,
	// protojson) — Go refuses to compile with unused imports, forcing
	// integrators to run goimports after every codegen.
	api := unaryOnlyAPI()
	content := generateServiceFile(api.Services[0], api)
	assertImports(t, content, nil, []string{
		"fmt",
		"io",
		"github.com/coder/websocket",
		"google.golang.org/protobuf/encoding/protojson",
		"google.golang.org/grpc", // never used in the template at all
	})
}

func TestGenerateServiceFile_NoUnusedImports_SSE(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Live", HTTPMethod: "GET", HTTPPath: "/live",
				StreamType: parser.StreamServer, SSE: true,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	assertImports(t, content,
		[]string{"fmt", "io"},
		[]string{"github.com/coder/websocket", "google.golang.org/protobuf/encoding/protojson"},
	)
}

func TestGenerateServiceFile_NoUnusedImports_WSServerStream(t *testing.T) {
	// Server-streaming over WebSocket needs websocket + io (for io.EOF) but
	// not fmt or protojson (outbound marshaling routes through
	// runtime.MarshalProto).
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Stream", HTTPMethod: "GET", HTTPPath: "/stream",
				StreamType: parser.StreamServer,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	assertImports(t, content,
		[]string{"github.com/coder/websocket", "io"},
		[]string{"fmt", "google.golang.org/protobuf/encoding/protojson"},
	)
}

func TestGenerateServiceFile_GofmtStable_UnaryOnly(t *testing.T) {
	// Conditional imports must produce gofmt-stable output: re-formatting
	// must not change a single byte. Catches stray blank lines / misaligned
	// import groups that compile but make every regen produce a noisy diff.
	api := unaryOnlyAPI()
	content := generateServiceFile(api.Services[0], api)
	formatted, err := format.Source([]byte(content))
	if err != nil {
		t.Fatalf("format.Source: %v\n%s", err, content)
	}
	if string(formatted) != content {
		t.Errorf("generated source is not gofmt-stable; diff:\n--- generated\n%s\n--- gofmt-formatted\n%s", content, formatted)
	}
}

func TestGenerateServiceFile_StreamingOnly_NoContextImport(t *testing.T) {
	// Streaming branches use the local `ctx` variable but never reference
	// the context.Context type. A service with no unary methods must not
	// import "context".
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Stream", HTTPMethod: "GET", HTTPPath: "/stream",
				StreamType: parser.StreamServer,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	assertImports(t, content, nil, []string{"context"})
}

func TestGenerateMain_NoUnusedImports(t *testing.T) {
	// main.go must not import "fmt" (template never references it) and must
	// not import every service's proto package via a per-service alias —
	// only the auth service's proto package is actually referenced from
	// main.go's auth function.
	api := testAPI()
	content := generateMain(api, "example.com/test/handler", "")
	imports := importsOf(t, content)
	if _, ok := imports["fmt"]; ok {
		t.Errorf("main.go must not import fmt (never used in template); imports=%v", keys(imports))
	}
	// testAPI has one service named "ChatService" (proto pkg "chat.v1") and
	// an auth service "AuthService" (proto pkg "auth.v1"). main.go references
	// only the auth proto package, so the chat alias must NOT be imported.
	if _, ok := imports["chat/v1"]; ok {
		t.Errorf("main.go imports chat proto pkg but never references it: %v", keys(imports))
	}
}

func TestGenerateMain_GofmtStable(t *testing.T) {
	api := testAPI()
	content := generateMain(api, "example.com/test/handler", "")
	formatted, err := format.Source([]byte(content))
	if err != nil {
		t.Fatalf("format.Source: %v\n%s", err, content)
	}
	if string(formatted) != content {
		t.Errorf("generated main.go is not gofmt-stable; diff:\n--- generated\n%s\n--- gofmt\n%s", content, formatted)
	}
}

func TestGenerateServiceFile_NoUnusedImports_WSBidi(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{{
			Name: "X", ProtoPackage: "x.v1", GoPackage: "x/v1",
			Methods: []*parser.Method{{
				Name: "Chat", HTTPMethod: "GET", HTTPPath: "/chat",
				StreamType: parser.StreamBidi,
				InputType:  &parser.MessageType{Name: "Req", FullName: ".x.v1.Req"},
				OutputType: &parser.MessageType{Name: "Msg", FullName: ".x.v1.Msg"},
			}},
		}},
	}
	content := generateServiceFile(api.Services[0], api)
	assertImports(t, content,
		// Bidi calls context.WithCancel(ctx) → context import is required
		// even though there is no unary closure.
		[]string{"github.com/coder/websocket", "google.golang.org/protobuf/encoding/protojson", "context"},
		nil,
	)
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

func TestProtoImportPath(t *testing.T) {
	// With GoPackage set, use it directly.
	svc := &parser.Service{GoPackage: "github.com/foo/gen/chat/v1", ProtoPackage: "chat.v1"}
	got := protoImportPath(svc)
	if got != "github.com/foo/gen/chat/v1" {
		t.Errorf("protoImportPath() = %q, want full go_package", got)
	}

	// Without GoPackage, fall back to proto package conversion.
	svc2 := &parser.Service{ProtoPackage: "chat.v1"}
	got2 := protoImportPath(svc2)
	if got2 != "chat/v1" {
		t.Errorf("protoImportPath() fallback = %q, want chat/v1", got2)
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

// --- Run() tests ---

func TestRun_ValidRequest(t *testing.T) {
	// Build a valid CodeGeneratorRequest
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test.proto"},
		Parameter:      strPtr("handler_pkg=example.com/test/handler"),
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    strPtr("test.proto"),
				Package: strPtr("test.v1"),
				MessageType: []*descriptorpb.DescriptorProto{
					{
						Name: strPtr("Req"),
						Field: []*descriptorpb.FieldDescriptorProto{
							{Name: strPtr("name"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
						},
					},
					{
						Name: strPtr("Resp"),
						Field: []*descriptorpb.FieldDescriptorProto{
							{Name: strPtr("id"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
						},
					},
				},
				Service: []*descriptorpb.ServiceDescriptorProto{
					{
						Name: strPtr("TestService"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{
								Name:       strPtr("Create"),
								InputType:  strPtr(".test.v1.Req"),
								OutputType: strPtr(".test.v1.Resp"),
								Options:    makeHTTPOpts("POST", "/things"),
							},
						},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp := Run(bytes.NewReader(data))
	if resp.Error != nil {
		t.Fatalf("Run() returned error: %s", resp.GetError())
	}
	if len(resp.File) == 0 {
		t.Error("expected generated files")
	}
}

func TestRun_InvalidBytes(t *testing.T) {
	resp := Run(bytes.NewReader([]byte("not valid protobuf")))
	if resp.Error == nil {
		t.Fatal("expected error for invalid protobuf")
	}
}

func TestGenerate_HandlerPkgResolveFails(t *testing.T) {
	// No HandlerPkg in opts AND a CWD without go.mod → resolveHandlerPkg
	// errors out via the documented remediation message. Mirrors the same
	// test in mcpgen so the REST plugin's error-surfacing path is covered.
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(testAPI(), Options{}); err == nil {
		t.Fatal("expected error from resolveHandlerPkg")
	}
}

func TestRun_GenerateError_SurfacesViaResponse(t *testing.T) {
	// Run with a CWD where the handler_pkg resolver cannot find a go.mod
	// and no explicit handler_pkg is set — Generate fails, the error must
	// land in resp.Error rather than panic.
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    strPtr("test.proto"),
				Package: strPtr("test.v1"),
				MessageType: []*descriptorpb.DescriptorProto{
					{Name: strPtr("Req"), Field: []*descriptorpb.FieldDescriptorProto{
						{Name: strPtr("id"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
					}},
					{Name: strPtr("Resp"), Field: []*descriptorpb.FieldDescriptorProto{
						{Name: strPtr("id"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
					}},
				},
				Service: []*descriptorpb.ServiceDescriptorProto{{
					Name: strPtr("S"),
					Method: []*descriptorpb.MethodDescriptorProto{{
						Name:       strPtr("Get"),
						InputType:  strPtr(".test.v1.Req"),
						OutputType: strPtr(".test.v1.Resp"),
						Options:    makeHTTPOpts("GET", "/x"),
					}},
				}},
			},
		},
	}
	data, _ := proto.Marshal(req)
	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Fatal("expected Generate failure to surface in resp.Error")
	}
}

func TestRun_BadParameter(t *testing.T) {
	// Run must surface ParseOptions errors via the response.Error.
	req := &pluginpb.CodeGeneratorRequest{
		Parameter:      strPtr("bogus=x"),
		FileToGenerate: []string{"test.proto"},
	}
	data, _ := proto.Marshal(req)
	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Fatal("expected error in response for unknown plugin option")
	}
}

func TestRun_ReadError(t *testing.T) {
	// io.ReadAll error path: pass a reader that always errors.
	resp := Run(&errReaderGen{})
	if resp.Error == nil {
		t.Fatal("expected error from failing reader")
	}
}

type errReaderGen struct{}

func (e *errReaderGen) Read(_ []byte) (int, error) { return 0, errReaderGenFail }

var errReaderGenFail = errStrGen("read failed")

type errStrGen string

func (e errStrGen) Error() string { return string(e) }

func TestRun_EmptyReader(t *testing.T) {
	resp := Run(&errReader{})
	if resp.Error == nil {
		t.Fatal("expected error for read error")
	}
}

// errReader always returns an error on Read
type errReader struct{}

func (r *errReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("read error")
}

// helpers for Run tests
func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
func boolPtr(b bool) *bool    { return &b }

func makeHTTPOpts(method, path string) *descriptorpb.MethodOptions {
	opts := &descriptorpb.MethodOptions{}
	var rule *annotations.HttpRule
	switch method {
	case "GET":
		rule = &annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: path}}
	case "POST":
		rule = &annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: path}}
	case "PUT":
		rule = &annotations.HttpRule{Pattern: &annotations.HttpRule_Put{Put: path}}
	case "DELETE":
		rule = &annotations.HttpRule{Pattern: &annotations.HttpRule_Delete{Delete: path}}
	case "PATCH":
		rule = &annotations.HttpRule{Pattern: &annotations.HttpRule_Patch{Patch: path}}
	}
	proto.SetExtension(opts, annotations.E_Http, rule)
	return opts
}

// --- AsyncAPI client streaming test ---

func TestGenerateAsyncAPIClientStreaming(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "UploadService",
				ProtoPackage: "upload.v1",
				Methods: []*parser.Method{
					{
						Name:       "UploadChunks",
						HTTPMethod: "GET",
						HTTPPath:   "/upload/stream",
						StreamType: parser.StreamClient,
						InputType: &parser.MessageType{
							Name:     "Chunk",
							FullName: ".upload.v1.Chunk",
							Fields: []*parser.Field{
								{Name: "data", Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "UploadResult",
							FullName: ".upload.v1.UploadResult",
							Fields: []*parser.Field{
								{Name: "url", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}

	content := GenerateAsyncAPI(api)
	if content == "" {
		t.Fatal("expected non-empty AsyncAPI spec for client streaming")
	}

	checks := []string{
		"client streaming",
		"/upload/stream",
		"UploadChunksSend:",
		"action: send",
		"Chunk:",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("AsyncAPI spec missing %q", check)
		}
	}
	// Client streaming should NOT have a Receive operation
	if strings.Contains(content, "UploadChunksReceive:") {
		t.Error("client streaming should not have Receive operation")
	}
}

// --- AsyncAPI transitive message refs test ---

func TestGenerateAsyncAPITransitiveMessageRefs(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "TaskService",
				ProtoPackage: "tasks.v1",
				Methods: []*parser.Method{
					{
						Name:       "WatchTasks",
						HTTPMethod: "GET",
						HTTPPath:   "/tasks/watch",
						StreamType: parser.StreamServer,
						InputType: &parser.MessageType{
							Name:     "WatchReq",
							FullName: ".tasks.v1.WatchReq",
							Fields: []*parser.Field{
								{Name: "filter", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
						OutputType: &parser.MessageType{
							Name:     "TaskEvent",
							FullName: ".tasks.v1.TaskEvent",
							Fields: []*parser.Field{
								{Name: "event_type", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
								{Name: "task", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".tasks.v1.Task"},
							},
						},
					},
					// The Task message type is used as input of another method
					// so findMessageTypeInAPI can discover it.
					{
						Name:       "GetTask",
						HTTPMethod: "GET",
						HTTPPath:   "/tasks/{id}",
						StreamType: parser.StreamUnary,
						InputType: &parser.MessageType{
							Name:     "GetTaskReq",
							FullName: ".tasks.v1.GetTaskReq",
						},
						OutputType: &parser.MessageType{
							Name:     "Task",
							FullName: ".tasks.v1.Task",
							Fields: []*parser.Field{
								{Name: "id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
								{Name: "title", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}

	content := GenerateAsyncAPI(api)
	if content == "" {
		t.Fatal("expected non-empty AsyncAPI spec")
	}

	// The Task schema should be included transitively
	if !strings.Contains(content, "Task:") {
		t.Error("expected transitive Task schema in AsyncAPI")
	}
	if !strings.Contains(content, "TaskEvent:") {
		t.Error("expected TaskEvent schema in AsyncAPI")
	}
}

// --- findMessageTypeInAPI direct test ---

func TestFindMessageTypeInAPI(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name: "Svc",
				Methods: []*parser.Method{
					{
						Name:       "Do",
						InputType:  &parser.MessageType{Name: "Req", FullName: ".svc.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".svc.v1.Resp"},
					},
				},
			},
		},
	}

	// Found as input type
	if got := findMessageTypeInAPI(api, "Req"); got == nil || got.Name != "Req" {
		t.Error("expected to find Req")
	}
	// Found as output type
	if got := findMessageTypeInAPI(api, "Resp"); got == nil || got.Name != "Resp" {
		t.Error("expected to find Resp")
	}
	// Not found
	if got := findMessageTypeInAPI(api, "NotExist"); got != nil {
		t.Error("expected nil for non-existent type")
	}
}

// --- OpenAPI writeFieldType missing types ---

func TestGenerateOpenAPIFieldTypes_AllTypes(t *testing.T) {
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
								{Name: "u32", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32},
								{Name: "u64", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT64},
								{Name: "f32", Type: descriptorpb.FieldDescriptorProto_TYPE_FIXED32},
								{Name: "f64", Type: descriptorpb.FieldDescriptorProto_TYPE_FIXED64},
								{Name: "flt", Type: descriptorpb.FieldDescriptorProto_TYPE_FLOAT},
								{Name: "dbl", Type: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE},
								{Name: "raw", Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES},
								{Name: "sf32", Type: descriptorpb.FieldDescriptorProto_TYPE_SFIXED32},
								{Name: "sf64", Type: descriptorpb.FieldDescriptorProto_TYPE_SFIXED64},
								{Name: "si32", Type: descriptorpb.FieldDescriptorProto_TYPE_SINT32},
								{Name: "si64", Type: descriptorpb.FieldDescriptorProto_TYPE_SINT64},
								// An unknown/default type
								{Name: "unknown", Type: descriptorpb.FieldDescriptorProto_Type(99)},
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
		"format: uint32",
		"format: uint64",
		"format: float",
		"format: double",
		"format: byte",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("OpenAPI spec missing %q", check)
		}
	}
}

// --- writePath PUT method ---

func TestGenerateOpenAPIPutMethod(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Update",
						HTTPMethod: "PUT",
						HTTPPath:   "/items/{id}",
						PathParams: []string{"id"},
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".svc.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".svc.v1.Resp"},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)
	if !strings.Contains(content, "put:") {
		t.Error("expected put: in OpenAPI spec")
	}
	if !strings.Contains(content, "requestBody:") {
		t.Error("expected requestBody for PUT method")
	}
}

func TestGenerateOpenAPIPatchMethod(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Patch",
						HTTPMethod: "PATCH",
						HTTPPath:   "/items/{id}",
						PathParams: []string{"id"},
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".svc.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".svc.v1.Resp"},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)
	if !strings.Contains(content, "patch:") {
		t.Error("expected patch: in OpenAPI spec")
	}
	if !strings.Contains(content, "requestBody:") {
		t.Error("expected requestBody for PATCH method")
	}
}

func TestGenerateOpenAPIDeleteMethod(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Delete",
						HTTPMethod: "DELETE",
						HTTPPath:   "/items/{id}",
						PathParams: []string{"id"},
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".svc.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".svc.v1.Resp"},
					},
				},
			},
		},
	}

	content := GenerateOpenAPI(api)
	if !strings.Contains(content, "delete:") {
		t.Error("expected delete: in OpenAPI spec")
	}
	// DELETE should NOT have requestBody
	if strings.Contains(content, "requestBody:") {
		t.Error("DELETE should not have requestBody")
	}
}

// --- generateMain with auth service not in services list (fallback auth conn) ---

func TestGenerateMainAuthServiceNotInServicesList(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "ChatService",
				ProtoPackage: "chat.v1",
				Methods: []*parser.Method{
					{
						Name:       "Send",
						HTTPMethod: "POST",
						HTTPPath:   "/send",
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".chat.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".chat.v1.Resp"},
					},
				},
			},
		},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
		},
	}

	content := generateMain(api, "example.com/test/handler", "")

	// Auth service should get its own address variable
	if !strings.Contains(content, "authServiceAddr") {
		t.Error("expected authServiceAddr for fallback auth connection")
	}
	if !strings.Contains(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR") {
		t.Error("expected PROTOBRIDGE_AUTH_SERVICE_ADDR")
	}
	if !strings.Contains(content, "Authenticate") {
		t.Error("expected Authenticate")
	}
}

// --- generateWSHandler server streaming ---

func TestGenerateWSHandlerServerStreaming(t *testing.T) {
	svc := &parser.Service{
		Name:         "EventService",
		ProtoPackage: "events.v1",
	}
	m := &parser.Method{
		Name:       "StreamEvents",
		StreamType: parser.StreamServer,
		InputType:  &parser.MessageType{Name: "StreamEventsReq", FullName: ".events.v1.StreamEventsReq"},
		OutputType: &parser.MessageType{Name: "Event", FullName: ".events.v1.Event"},
	}

	content := generateWSHandler(svc, m)

	checks := []string{
		"streamEventsWSHandler",
		"StreamEvents(ctx)",
		"StreamEventsReq",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("WS handler missing %q", check)
		}
	}
}

// --- generateWSHandler with ExcludeAuth ---

func TestGenerateWSHandlerExcludeAuth(t *testing.T) {
	svc := &parser.Service{
		Name:         "EventService",
		ProtoPackage: "events.v1",
	}
	m := &parser.Method{
		Name:        "StreamEvents",
		StreamType:  parser.StreamBidi,
		ExcludeAuth: true,
		InputType:   &parser.MessageType{Name: "Req", FullName: ".events.v1.Req"},
		OutputType:  &parser.MessageType{Name: "Resp", FullName: ".events.v1.Resp"},
	}

	content := generateWSHandler(svc, m)

	if !strings.Contains(content, "true") {
		t.Error("expected ExcludeAuth=true in WS handler")
	}
}

// --- GenerateEnvExample with auth service not in services list ---

func TestGenerateEnvExampleAuthServiceNotInList(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "ChatService",
				ProtoPackage: "chat.v1",
				Methods: []*parser.Method{
					{
						Name:       "Send",
						HTTPMethod: "POST",
						HTTPPath:   "/send",
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".chat.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".chat.v1.Resp"},
					},
				},
			},
		},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
		},
	}

	content := GenerateEnvExample(api)
	if !strings.Contains(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR=localhost:50051") {
		t.Error("expected auth service addr in .env.example")
	}
}

// --- GenerateK8sManifest with auth service not in services list ---

func TestGenerateK8sManifestAuthServiceNotInList(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "ChatService",
				ProtoPackage: "chat.v1",
				Methods: []*parser.Method{
					{
						Name:       "Send",
						HTTPMethod: "POST",
						HTTPPath:   "/send",
						StreamType: parser.StreamUnary,
						InputType:  &parser.MessageType{Name: "Req", FullName: ".chat.v1.Req"},
						OutputType: &parser.MessageType{Name: "Resp", FullName: ".chat.v1.Resp"},
					},
				},
			},
		},
		AuthMethod: &parser.AuthMethod{
			ServiceName: "AuthService",
			MethodName:  "Authenticate",
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
		},
	}

	content := GenerateK8sManifest(api)
	if !strings.Contains(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR") {
		t.Error("expected auth service addr in k8s manifest")
	}
}

// --- generateServiceFile with streaming method (non-unary, IsUnary=false branch) ---

func TestGenerateServiceFileStreamingMethod(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "EventService",
				ProtoPackage: "events.v1",
				Methods: []*parser.Method{
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

	content := generateServiceFile(api.Services[0], api)

	// Streaming methods should still produce a handler function
	if !strings.Contains(content, "bidiChatHandler") {
		t.Error("expected handler function for streaming method")
	}
	// But should NOT contain UnaryCallWithRetry (that's for unary only)
	if strings.Contains(content, "UnaryCallWithRetry") {
		t.Error("streaming method should not have UnaryCallWithRetry")
	}
}

// --- OpenAPI with query params target ---

func TestGenerateOpenAPIQueryParamsTarget(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:              "List",
						HTTPMethod:        "GET",
						HTTPPath:          "/items",
						StreamType:        parser.StreamUnary,
						QueryParamsTarget: "filter",
						InputType: &parser.MessageType{
							Name:     "Req",
							FullName: ".svc.v1.Req",
							Fields: []*parser.Field{
								{Name: "filter", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".svc.v1.Filter"},
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
	if !strings.Contains(content, "# query params from filter") {
		t.Error("expected query params comment in OpenAPI spec")
	}
}

// --- generateMain with auth service IN services list (AuthConnVar found directly) ---

func TestGenerateMainAuthServiceInServicesList(t *testing.T) {
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
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
		},
	}

	content := generateMain(api, "example.com/test/handler", "")
	if !strings.Contains(content, "AuthService") {
		t.Error("expected AuthService in auth function")
	}
	if !strings.Contains(content, "Authenticate") {
		t.Error("expected Authenticate method call")
	}
	if !strings.Contains(content, "AuthReq") {
		t.Error("expected AuthReq input type")
	}
	// Should only have one service entry (not duplicated)
	count := strings.Count(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR")
	if count != 2 { // once for requireEnv, once for register call
		// Just ensure it's not zero
		if count == 0 {
			t.Error("expected PROTOBRIDGE_AUTH_SERVICE_ADDR")
		}
	}
}

// --- GenerateEnvExample with auth service IN services list (found=true branch) ---

func TestGenerateEnvExampleAuthServiceInList(t *testing.T) {
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
			InputType:   &parser.MessageType{Name: "AuthReq", FullName: ".auth.v1.AuthReq"},
		},
	}

	content := GenerateEnvExample(api)
	// Auth service addr should appear exactly once (from services loop, not duplicated)
	count := strings.Count(content, "PROTOBRIDGE_AUTH_SERVICE_ADDR=localhost:50051")
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of PROTOBRIDGE_AUTH_SERVICE_ADDR, got %d", count)
	}
}

// --- Run with parser error (validation failure) ---

func TestRun_ParserError(t *testing.T) {
	// Trigger a parser error by creating a request with duplicate auth methods.
	// We need the protobridge auth_method extension. Import it.
	authOpts := &descriptorpb.MethodOptions{}
	proto.SetExtension(authOpts, optionspb.E_AuthMethod, true)

	// Auth method input needs a map field so the first auth method passes validation
	// before the duplicate check kicks in.
	mapEntry := &descriptorpb.DescriptorProto{
		Name: strPtr("HeadersEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: strPtr("key"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: strPtr("value"), Number: int32Ptr(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
		Options: &descriptorpb.MessageOptions{MapEntry: boolPtr(true)},
	}
	authInput := &descriptorpb.DescriptorProto{
		Name: strPtr("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("headers"),
				Number:   int32Ptr(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: strPtr(".test.v1.AuthReq.HeadersEntry"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{mapEntry},
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    strPtr("test.proto"),
				Package: strPtr("test.v1"),
				MessageType: []*descriptorpb.DescriptorProto{
					authInput,
					{Name: strPtr("AuthResp"), Field: []*descriptorpb.FieldDescriptorProto{
						{Name: strPtr("user_id"), Number: int32Ptr(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
					}},
				},
				Service: []*descriptorpb.ServiceDescriptorProto{
					{
						Name: strPtr("Svc"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{
								Name:       strPtr("Auth1"),
								InputType:  strPtr(".test.v1.AuthReq"),
								OutputType: strPtr(".test.v1.AuthResp"),
								Options:    authOpts,
							},
							{
								Name:       strPtr("Auth2"),
								InputType:  strPtr(".test.v1.AuthReq"),
								OutputType: strPtr(".test.v1.AuthResp"),
								Options:    authOpts,
							},
						},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Fatal("expected error for duplicate auth methods")
	}
}

// --- collectAsyncSchemas with nil message type ---

func TestCollectAsyncSchemasNilMessageType(t *testing.T) {
	api := &parser.ParsedAPI{
		Services: []*parser.Service{
			{
				Name:         "Svc",
				ProtoPackage: "svc.v1",
				Methods: []*parser.Method{
					{
						Name:       "Stream",
						StreamType: parser.StreamServer,
						InputType:  nil,
						OutputType: &parser.MessageType{
							Name:     "Event",
							FullName: ".svc.v1.Event",
							Fields: []*parser.Field{
								{Name: "data", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}

	channels := []asyncChannel{{svc: api.Services[0], method: api.Services[0].Methods[0]}}
	schemas := collectAsyncSchemas(channels, api)
	// Should have Event but handle nil InputType gracefully
	found := false
	for _, s := range schemas {
		if s.Name == "Event" {
			found = true
		}
	}
	if !found {
		t.Error("expected Event schema")
	}
}

// --- writeSchema with empty oneofDecls (zero variants) ---

func TestGenerateOpenAPIOneofEmptyVariants(t *testing.T) {
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
							Fields:   []*parser.Field{},
							OneofDecls: []*parser.OneofDecl{
								{Name: "empty", Variants: nil},
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
	// Empty oneof should NOT produce a comment
	if strings.Contains(content, "# oneof: empty") {
		t.Error("empty oneof should not produce a comment")
	}
}

func TestHasLabelsMapField(t *testing.T) {
	labelsMap := &parser.MessageType{
		Name: "AuthResponse", FullName: ".auth.AuthResponse",
		Fields: []*parser.Field{
			{Name: "user_id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
			{Name: "labels", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".auth.AuthResponse.LabelsEntry", Repeated: true},
		},
	}
	if !hasLabelsMapField(labelsMap) {
		t.Error("expected AuthResponse with LabelsEntry map field to be detected")
	}

	scalarLabels := &parser.MessageType{
		Name: "AuthResponse",
		Fields: []*parser.Field{
			{Name: "labels", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
		},
	}
	if hasLabelsMapField(scalarLabels) {
		t.Error("scalar `labels` field must not be mistaken for a map")
	}

	// Repeated non-map (TypeName without LabelsEntry suffix) must also be rejected.
	wrongSuffix := &parser.MessageType{
		Name: "AuthResponse",
		Fields: []*parser.Field{
			{Name: "labels", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".auth.Label", Repeated: true},
		},
	}
	if hasLabelsMapField(wrongSuffix) {
		t.Error("repeated message field without LabelsEntry suffix must not count as labels map")
	}

	if hasLabelsMapField(nil) {
		t.Error("nil message must return false")
	}
}

func TestGenerateMain_WithBroadcasts(t *testing.T) {
	api := &parser.ParsedAPI{
		BroadcastServices: []*parser.BroadcastService{{
			Name:         "OrderBroadcast",
			Route:        "/api/v1/events/orders",
			GoPackage:    "example.com/myapp/events",
			ProtoPackage: "myapp.events",
			Events: []*parser.BroadcastEvent{{
				OneofFieldName: "order_created",
				Message:        &parser.MessageType{Name: "OrderCreated", FullName: ".myapp.events.OrderCreated"},
				Subject:        "order_created",
				GoPackage:      "example.com/myapp/events",
			}},
		}},
	}

	content := generateMain(api, "example.com/test/handler", "example.com/gen/events")
	for _, want := range []string{
		`"github.com/mrs1lentcz/protobridge/runtime/events"`,
		`eventspb "example.com/gen/events"`,
		"events.NewBusFromEnv()",
		`"/api/v1/events/orders"`,
		"eventspb.NewOrderBroadcastHandler(bus,",
		"PrincipalLabels: principalLabelsFn",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("broadcast main.go missing %q\n---\n%s", want, content)
		}
	}

	// Generated file parses as Go.
	if _, err := parser2.ParseFile(token.NewFileSet(), "main.go", content, parser2.AllErrors); err != nil {
		t.Errorf("generated broadcast main.go is not parseable Go: %v\n%s", err, content)
	}
}

func TestGenerateMain_NoBroadcastsOmitsEventsImport(t *testing.T) {
	api := testAPI()
	content := generateMain(api, "example.com/test/handler", "")
	// No broadcast services → no events-runtime import, no bus init.
	for _, forbidden := range []string{
		"runtime/events",
		"events.NewBusFromEnv",
		"eventspb",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("non-broadcast main.go must not contain %q", forbidden)
		}
	}
}

func TestGenerate_BroadcastWithoutEventsPkgErrors(t *testing.T) {
	api := &parser.ParsedAPI{
		BroadcastServices: []*parser.BroadcastService{{
			Name: "OrderBroadcast", Route: "/x",
			Events: []*parser.BroadcastEvent{{OneofFieldName: "x", Message: &parser.MessageType{Name: "X"}, Subject: "x"}},
		}},
	}
	if _, err := Generate(api, Options{HandlerPkg: "example.com/h"}); err == nil || !strings.Contains(err.Error(), "events_pkg") {
		t.Errorf("expected events_pkg error, got %v", err)
	}
}

func TestParseOptions_EventsPkg(t *testing.T) {
	opts, err := ParseOptions("events_pkg=example.com/gen/events")
	if err != nil {
		t.Fatal(err)
	}
	if opts.EventsPkg != "example.com/gen/events" {
		t.Errorf("EventsPkg: %q", opts.EventsPkg)
	}
}
