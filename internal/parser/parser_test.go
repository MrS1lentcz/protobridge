package parser

import (
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// helper: pointer to string
func sp(s string) *string { return &s }

// helper: pointer to int32
func i32p(i int32) *int32 { return &i }

// helper: pointer to bool
func bp(b bool) *bool { return &b }

// methodOpts builds a MethodOptions with the given google.api.http rule.
func httpMethodOpts(method, path string) *descriptorpb.MethodOptions {
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

// mergeMethodOpts merges custom protobridge extensions into existing method options.
func withAuthMethod(opts *descriptorpb.MethodOptions) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_AuthMethod, true)
	return opts
}

func withRequiredHeaders(opts *descriptorpb.MethodOptions, headers []string) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_RequiredHeaders, headers)
	return opts
}

func withQueryParamsTarget(opts *descriptorpb.MethodOptions, target string) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_QueryParamsTarget, target)
	return opts
}

func withExcludeAuth(opts *descriptorpb.MethodOptions) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_ExcludeAuth, true)
	return opts
}

func withSSE(opts *descriptorpb.MethodOptions) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_Sse, true)
	return opts
}

func withWSMode(opts *descriptorpb.MethodOptions, mode string) *descriptorpb.MethodOptions {
	if opts == nil {
		opts = &descriptorpb.MethodOptions{}
	}
	proto.SetExtension(opts, optionspb.E_WsMode, mode)
	return opts
}

func serviceOptsPathPrefix(prefix string) *descriptorpb.ServiceOptions {
	opts := &descriptorpb.ServiceOptions{}
	proto.SetExtension(opts, optionspb.E_PathPrefix, prefix)
	return opts
}

func serviceOptsBoth(displayName, pathPrefix string) *descriptorpb.ServiceOptions {
	opts := &descriptorpb.ServiceOptions{}
	proto.SetExtension(opts, optionspb.E_DisplayName, displayName)
	proto.SetExtension(opts, optionspb.E_PathPrefix, pathPrefix)
	return opts
}

// makeSimpleMessage creates a DescriptorProto with string fields.
func makeSimpleMessage(name string, fields ...string) *descriptorpb.DescriptorProto {
	msg := &descriptorpb.DescriptorProto{Name: sp(name)}
	for i, f := range fields {
		msg.Field = append(msg.Field, &descriptorpb.FieldDescriptorProto{
			Name:   sp(f),
			Number: i32p(int32(i + 1)),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		})
	}
	return msg
}

// makeRequest builds a CodeGeneratorRequest with one file, messages, and one service.
func makeRequest(
	pkg string,
	fileName string,
	msgs []*descriptorpb.DescriptorProto,
	enums []*descriptorpb.EnumDescriptorProto,
	svcs []*descriptorpb.ServiceDescriptorProto,
) *pluginpb.CodeGeneratorRequest {
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{fileName},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:        sp(fileName),
				Package:     sp(pkg),
				MessageType: msgs,
				EnumType:    enums,
				Service:     svcs,
			},
		},
	}
}

func TestParseBasicUnaryRPC(t *testing.T) {
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("CreateReq", "name"),
			makeSimpleMessage("CreateResp", "id"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("TestService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Create"),
						InputType:  sp(".test.v1.CreateReq"),
						OutputType: sp(".test.v1.CreateResp"),
						Options:    httpMethodOpts("POST", "/things"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(api.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(api.Services))
	}
	svc := api.Services[0]
	if svc.Name != "TestService" {
		t.Errorf("service name = %q, want TestService", svc.Name)
	}
	if len(svc.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(svc.Methods))
	}
	m := svc.Methods[0]
	if m.Name != "Create" {
		t.Errorf("method name = %q, want Create", m.Name)
	}
	if m.HTTPMethod != "POST" {
		t.Errorf("HTTPMethod = %q, want POST", m.HTTPMethod)
	}
	if m.HTTPPath != "/things" {
		t.Errorf("HTTPPath = %q, want /things", m.HTTPPath)
	}
	if m.InputType == nil || m.InputType.Name != "CreateReq" {
		t.Errorf("InputType.Name = %v, want CreateReq", m.InputType)
	}
	if m.OutputType == nil || m.OutputType.Name != "CreateResp" {
		t.Errorf("OutputType.Name = %v, want CreateResp", m.OutputType)
	}
	if m.StreamType != StreamUnary {
		t.Errorf("StreamType = %v, want StreamUnary", m.StreamType)
	}
}

func TestParseNoHTTPAnnotationSkipsMethod(t *testing.T) {
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Internal"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						// No HTTP annotation, no Options at all.
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	// Service with no methods should not be in the output.
	if len(api.Services) != 0 {
		t.Errorf("expected 0 services (no HTTP methods), got %d", len(api.Services))
	}
}

func TestParseAuthMethod(t *testing.T) {
	// Auth method input needs a map field so validation passes.
	mapEntry := &descriptorpb.DescriptorProto{
		Name: sp("HeadersEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("key"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: sp("value"), Number: i32p(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
		Options: &descriptorpb.MessageOptions{MapEntry: bp(true)},
	}
	authInput := &descriptorpb.DescriptorProto{
		Name: sp("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("headers"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: sp(".test.v1.AuthReq.HeadersEntry"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{mapEntry},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			authInput,
			makeSimpleMessage("AuthResp", "user_id"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("AuthService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Authenticate"),
						InputType:  sp(".test.v1.AuthReq"),
						OutputType: sp(".test.v1.AuthResp"),
						Options:    withAuthMethod(nil),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if api.AuthMethod == nil {
		t.Fatal("expected AuthMethod to be set")
	}
	if api.AuthMethod.ServiceName != "AuthService" {
		t.Errorf("AuthMethod.ServiceName = %q, want AuthService", api.AuthMethod.ServiceName)
	}
	if api.AuthMethod.MethodName != "Authenticate" {
		t.Errorf("AuthMethod.MethodName = %q, want Authenticate", api.AuthMethod.MethodName)
	}
}

func TestParseAuthMethod_WithHTTPAnnotation_AlsoExposedAsREST(t *testing.T) {
	// When (protobridge.auth_method) is combined with (google.api.http), the
	// auth method must also be emitted as a regular REST method so callers
	// can hit the login endpoint directly. ExcludeAuth must be implicit so
	// the auth middleware does not try to authenticate the auth call itself.
	mapEntry := &descriptorpb.DescriptorProto{
		Name: sp("HeadersEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("key"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: sp("value"), Number: i32p(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
		Options: &descriptorpb.MessageOptions{MapEntry: bp(true)},
	}
	authInput := &descriptorpb.DescriptorProto{
		Name: sp("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name: sp("headers"), Number: i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: sp(".test.v1.AuthReq.HeadersEntry"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{mapEntry},
	}

	opts := withAuthMethod(nil)
	proto.SetExtension(opts, annotations.E_Http, &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Post{Post: "/auth/login"},
		Body:    "*",
	})

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			authInput,
			makeSimpleMessage("AuthResp", "user_id"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("AuthService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Authenticate"),
						InputType:  sp(".test.v1.AuthReq"),
						OutputType: sp(".test.v1.AuthResp"),
						Options:    opts,
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if api.AuthMethod == nil {
		t.Fatal("expected AuthMethod to be set")
	}
	if len(api.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(api.Services))
	}
	svc := api.Services[0]
	if len(svc.Methods) != 1 {
		t.Fatalf("expected the auth method exposed as REST, got %d methods", len(svc.Methods))
	}
	m := svc.Methods[0]
	if m.HTTPMethod != "POST" || m.HTTPPath != "/auth/login" {
		t.Errorf("got %s %s, want POST /auth/login", m.HTTPMethod, m.HTTPPath)
	}
	if !m.ExcludeAuth {
		t.Error("auth method exposed as REST must have ExcludeAuth=true (no middleware on login)")
	}
}

func TestParseMultipleAuthMethodsError(t *testing.T) {
	mapEntry := &descriptorpb.DescriptorProto{
		Name: sp("HeadersEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("key"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: sp("value"), Number: i32p(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
		Options: &descriptorpb.MessageOptions{MapEntry: bp(true)},
	}
	authInput := &descriptorpb.DescriptorProto{
		Name: sp("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("headers"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: sp(".test.v1.AuthReq.HeadersEntry"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{mapEntry},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			authInput,
			makeSimpleMessage("AuthResp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Auth1"),
						InputType:  sp(".test.v1.AuthReq"),
						OutputType: sp(".test.v1.AuthResp"),
						Options:    withAuthMethod(nil),
					},
					{
						Name:       sp("Auth2"),
						InputType:  sp(".test.v1.AuthReq"),
						OutputType: sp(".test.v1.AuthResp"),
						Options:    withAuthMethod(nil),
					},
				},
			},
		},
	)

	_, err := Parse(req)
	if err == nil {
		t.Fatal("expected error for multiple auth methods")
	}
	if got := err.Error(); !contains(got, "multiple auth_method") {
		t.Errorf("error = %q, want it to contain 'multiple auth_method'", got)
	}
}

func TestParseStreamTypes(t *testing.T) {
	tests := []struct {
		name           string
		clientStream   bool
		serverStream   bool
		wantStreamType StreamType
	}{
		{"unary", false, false, StreamUnary},
		{"server_streaming", false, true, StreamServer},
		{"client_streaming", true, false, StreamClient},
		{"bidi_streaming", true, true, StreamBidi},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeRequest("test.v1", "test.proto",
				[]*descriptorpb.DescriptorProto{
					makeSimpleMessage("Req"),
					makeSimpleMessage("Resp"),
				},
				nil,
				[]*descriptorpb.ServiceDescriptorProto{
					{
						Name: sp("Svc"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{
								Name:            sp("Stream"),
								InputType:       sp(".test.v1.Req"),
								OutputType:      sp(".test.v1.Resp"),
								Options:         httpMethodOpts("GET", "/stream"),
								ClientStreaming: bp(tt.clientStream),
								ServerStreaming: bp(tt.serverStream),
							},
						},
					},
				},
			)

			api, err := Parse(req)
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			if len(api.Services) != 1 || len(api.Services[0].Methods) != 1 {
				t.Fatal("expected exactly 1 service with 1 method")
			}
			if got := api.Services[0].Methods[0].StreamType; got != tt.wantStreamType {
				t.Errorf("StreamType = %v, want %v", got, tt.wantStreamType)
			}
		})
	}
}

func TestParsePathParamExtraction(t *testing.T) {
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Get"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("GET", "/users/{user_id}/posts/{post_id}"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	if len(m.PathParams) != 2 {
		t.Fatalf("expected 2 path params, got %d", len(m.PathParams))
	}
	if m.PathParams[0] != "user_id" || m.PathParams[1] != "post_id" {
		t.Errorf("PathParams = %v, want [user_id, post_id]", m.PathParams)
	}
}

func TestParseRequiredHeaders(t *testing.T) {
	opts := httpMethodOpts("POST", "/test")
	opts = withRequiredHeaders(opts, []string{"X-Request-Id", "X-Tenant"})

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Do"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    opts,
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	if len(m.RequiredHeaders) != 2 {
		t.Fatalf("expected 2 required headers, got %d", len(m.RequiredHeaders))
	}
	if m.RequiredHeaders[0] != "X-Request-Id" || m.RequiredHeaders[1] != "X-Tenant" {
		t.Errorf("RequiredHeaders = %v", m.RequiredHeaders)
	}
}

func TestParseQueryParamsTarget(t *testing.T) {
	opts := httpMethodOpts("GET", "/items")
	opts = withQueryParamsTarget(opts, "filter")

	// filter is a nested message field
	filterMsg := makeSimpleMessage("Filter", "status")
	inputMsg := &descriptorpb.DescriptorProto{
		Name: sp("ListReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("filter"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: sp(".test.v1.Filter"),
			},
		},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			inputMsg,
			filterMsg,
			makeSimpleMessage("ListResp", "items"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("List"),
						InputType:  sp(".test.v1.ListReq"),
						OutputType: sp(".test.v1.ListResp"),
						Options:    opts,
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	if m.QueryParamsTarget != "filter" {
		t.Errorf("QueryParamsTarget = %q, want filter", m.QueryParamsTarget)
	}
}

func TestParseExcludeAuth(t *testing.T) {
	opts := httpMethodOpts("GET", "/public")
	opts = withExcludeAuth(opts)

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Public"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    opts,
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !api.Services[0].Methods[0].ExcludeAuth {
		t.Error("ExcludeAuth should be true")
	}
}

func TestParseSSE(t *testing.T) {
	opts := httpMethodOpts("GET", "/events")
	opts = withSSE(opts)

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:            sp("Events"),
						InputType:       sp(".test.v1.Req"),
						OutputType:      sp(".test.v1.Resp"),
						Options:         opts,
						ServerStreaming: bp(true),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !api.Services[0].Methods[0].SSE {
		t.Error("SSE should be true")
	}
}

func TestParseWSMode(t *testing.T) {
	opts := httpMethodOpts("GET", "/ws")
	opts = withWSMode(opts, "broadcast")

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:            sp("Watch"),
						InputType:       sp(".test.v1.Req"),
						OutputType:      sp(".test.v1.Resp"),
						Options:         opts,
						ServerStreaming: bp(true),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if api.Services[0].Methods[0].WSMode != "broadcast" {
		t.Errorf("WSMode = %q, want broadcast", api.Services[0].Methods[0].WSMode)
	}
}

func TestParseServiceDisplayNameAndPathPrefix(t *testing.T) {
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name:    sp("ChatService"),
				Options: serviceOptsBoth("Chat", "/api/v1"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Send"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("POST", "/messages"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	svc := api.Services[0]
	if svc.DisplayName != "Chat" {
		t.Errorf("DisplayName = %q, want Chat", svc.DisplayName)
	}
	if svc.PathPrefix != "/api/v1" {
		t.Errorf("PathPrefix = %q, want /api/v1", svc.PathPrefix)
	}
	// Path prefix should be applied to method paths.
	if svc.Methods[0].HTTPPath != "/api/v1/messages" {
		t.Errorf("HTTPPath = %q, want /api/v1/messages", svc.Methods[0].HTTPPath)
	}
}

func TestParseAllHTTPMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, httpMethod := range methods {
		t.Run(httpMethod, func(t *testing.T) {
			req := makeRequest("test.v1", "test.proto",
				[]*descriptorpb.DescriptorProto{
					makeSimpleMessage("Req"),
					makeSimpleMessage("Resp"),
				},
				nil,
				[]*descriptorpb.ServiceDescriptorProto{
					{
						Name: sp("Svc"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{
								Name:       sp("Do"),
								InputType:  sp(".test.v1.Req"),
								OutputType: sp(".test.v1.Resp"),
								Options:    httpMethodOpts(httpMethod, "/test"),
							},
						},
					},
				},
			)

			api, err := Parse(req)
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			if api.Services[0].Methods[0].HTTPMethod != httpMethod {
				t.Errorf("HTTPMethod = %q, want %q", api.Services[0].Methods[0].HTTPMethod, httpMethod)
			}
		})
	}
}

func TestParseEnumFieldValues(t *testing.T) {
	enumDesc := &descriptorpb.EnumDescriptorProto{
		Name: sp("Status"),
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: sp("STATUS_UNSPECIFIED"), Number: i32p(0)},
			{Name: sp("STATUS_ACTIVE"), Number: i32p(1)},
			{Name: sp("STATUS_INACTIVE"), Number: i32p(2)},
		},
	}

	inputMsg := &descriptorpb.DescriptorProto{
		Name: sp("Req"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("status"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: sp(".test.v1.Status"),
			},
		},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			inputMsg,
			makeSimpleMessage("Resp"),
		},
		[]*descriptorpb.EnumDescriptorProto{enumDesc},
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Do"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("GET", "/test"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	statusField := m.InputType.Fields[0]
	// 0-value is excluded
	if len(statusField.EnumValues) != 2 {
		t.Fatalf("expected 2 enum values (excl 0), got %d", len(statusField.EnumValues))
	}
	if statusField.EnumValues[0].Name != "STATUS_ACTIVE" {
		t.Errorf("enum value[0] = %q, want STATUS_ACTIVE", statusField.EnumValues[0].Name)
	}
}

func TestParsePathParamExtractionFromPrefixedPath(t *testing.T) {
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name:    sp("Svc"),
				Options: serviceOptsPathPrefix("/api/v1"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Get"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("GET", "/items/{item_id}"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	if m.HTTPPath != "/api/v1/items/{item_id}" {
		t.Errorf("HTTPPath = %q, want /api/v1/items/{item_id}", m.HTTPPath)
	}
	if len(m.PathParams) != 1 || m.PathParams[0] != "item_id" {
		t.Errorf("PathParams = %v, want [item_id]", m.PathParams)
	}
}

func TestParseFileNotInGenerateListSkipped(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"wanted.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{
				Name:    sp("unwanted.proto"),
				Package: sp("pkg"),
				Service: []*descriptorpb.ServiceDescriptorProto{
					{
						Name: sp("Svc"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{
								Name:       sp("Do"),
								InputType:  sp(".pkg.Req"),
								OutputType: sp(".pkg.Resp"),
								Options:    httpMethodOpts("GET", "/x"),
							},
						},
					},
				},
				MessageType: []*descriptorpb.DescriptorProto{
					makeSimpleMessage("Req"),
					makeSimpleMessage("Resp"),
				},
			},
			{
				Name:    sp("wanted.proto"),
				Package: sp("pkg"),
			},
		},
	}

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(api.Services) != 0 {
		t.Error("expected no services from unwanted file")
	}
}

func TestParseOneofDecls(t *testing.T) {
	inputMsg := &descriptorpb.DescriptorProto{
		Name: sp("Req"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:       sp("text"),
				Number:     i32p(1),
				Type:       descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName:   sp(".test.v1.TextPayload"),
				OneofIndex: i32p(0),
			},
			{
				Name:       sp("image"),
				Number:     i32p(2),
				Type:       descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName:   sp(".test.v1.ImagePayload"),
				OneofIndex: i32p(0),
			},
		},
		OneofDecl: []*descriptorpb.OneofDescriptorProto{
			{Name: sp("payload")},
		},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			inputMsg,
			makeSimpleMessage("TextPayload", "text"),
			makeSimpleMessage("ImagePayload", "url"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Send"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("POST", "/send"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	m := api.Services[0].Methods[0]
	if len(m.InputType.OneofDecls) != 1 {
		t.Fatalf("expected 1 oneof decl, got %d", len(m.InputType.OneofDecls))
	}
	od := m.InputType.OneofDecls[0]
	if od.Name != "payload" {
		t.Errorf("oneof name = %q, want payload", od.Name)
	}
	if len(od.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(od.Variants))
	}
	if od.Variants[0].FieldName != "text" || od.Variants[0].MessageName != "TextPayload" {
		t.Errorf("variant[0] = %+v", od.Variants[0])
	}
	if od.Variants[1].FieldName != "image" || od.Variants[1].MessageName != "ImagePayload" {
		t.Errorf("variant[1] = %+v", od.Variants[1])
	}
}

// helper
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Tests for nil-options guard clauses in options.go ---

func TestGetRequiredHeaders_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getRequiredHeaders(m); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetQueryParamsTarget_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getQueryParamsTarget(m); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetExcludeAuth_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getExcludeAuth(m); got {
		t.Error("expected false")
	}
}

func TestGetAuthMethod_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getAuthMethod(m); got {
		t.Error("expected false")
	}
}

func TestGetFieldRequired_NilOptions(t *testing.T) {
	f := &descriptorpb.FieldDescriptorProto{Name: sp("test")}
	if got := getFieldRequired(f); got {
		t.Error("expected false")
	}
}

func TestGetSSE_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getSSE(m); got {
		t.Error("expected false")
	}
}

func TestGetWSMode_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	if got := getWSMode(m); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetDisplayName_NilOptions(t *testing.T) {
	s := &descriptorpb.ServiceDescriptorProto{Name: sp("Test")}
	if got := getDisplayName(s); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetPathPrefix_NilOptions(t *testing.T) {
	s := &descriptorpb.ServiceDescriptorProto{Name: sp("Test")}
	if got := getPathPrefix(s); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetMCP_NotSet(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("M")}
	val, set := getMCP(m)
	if val || set {
		t.Errorf("expected (false,false), got (%v,%v)", val, set)
	}
}

func TestGetMCP_ExplicitTrue(t *testing.T) {
	opts := &descriptorpb.MethodOptions{}
	proto.SetExtension(opts, optionspb.E_Mcp, true)
	m := &descriptorpb.MethodDescriptorProto{Name: sp("M"), Options: opts}
	val, set := getMCP(m)
	if !val || !set {
		t.Errorf("expected (true,true), got (%v,%v)", val, set)
	}
}

func TestGetMCP_ExplicitFalse(t *testing.T) {
	// Important: an explicit (mcp) = false must report set=true so the
	// service-level mcp_default opt-in can be overridden per method.
	opts := &descriptorpb.MethodOptions{}
	proto.SetExtension(opts, optionspb.E_Mcp, false)
	m := &descriptorpb.MethodDescriptorProto{Name: sp("M"), Options: opts}
	val, set := getMCP(m)
	if val {
		t.Error("val should be false")
	}
	if !set {
		t.Error("set should be true (explicit opt-out)")
	}
}

func TestParseMCP_DefaultOptInWithPerMethodOptOut(t *testing.T) {
	// Service has mcp_default=true; one method opts out via (mcp)=false.
	svcOpts := &descriptorpb.ServiceOptions{}
	proto.SetExtension(svcOpts, optionspb.E_McpDefault, true)

	optOut := &descriptorpb.MethodOptions{}
	proto.SetExtension(optOut, optionspb.E_Mcp, false)

	scopeOpts := &descriptorpb.MethodOptions{}
	proto.SetExtension(scopeOpts, optionspb.E_McpScope, "chat session")

	// Both methods need an HTTP rule so they're emitted as REST methods
	// (parser only attaches MCP attrs to methods that survive HTTP filtering).
	addHTTP := func(opts *descriptorpb.MethodOptions, path string) *descriptorpb.MethodOptions {
		proto.SetExtension(opts, annotations.E_Http, &annotations.HttpRule{
			Pattern: &annotations.HttpRule_Get{Get: path},
		})
		return opts
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req", "id"),
			makeSimpleMessage("Resp", "id"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("S"), Options: svcOpts,
				Method: []*descriptorpb.MethodDescriptorProto{
					{Name: sp("Inherited"),
						InputType: sp(".test.v1.Req"), OutputType: sp(".test.v1.Resp"),
						Options: addHTTP(scopeOpts, "/inherited")},
					{Name: sp("OptedOut"),
						InputType: sp(".test.v1.Req"), OutputType: sp(".test.v1.Resp"),
						Options: addHTTP(optOut, "/opted-out")},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(api.Services) != 1 {
		t.Fatalf("services: %d", len(api.Services))
	}
	svc := api.Services[0]
	if !svc.MCPDefault {
		t.Error("MCPDefault should be true on service")
	}
	byName := map[string]*Method{}
	for _, m := range svc.Methods {
		byName[m.Name] = m
	}
	if !byName["Inherited"].MCP {
		t.Error("Inherited method should inherit MCP=true from mcp_default")
	}
	if byName["OptedOut"].MCP {
		t.Error("OptedOut method must respect explicit (mcp)=false")
	}
	if !byName["OptedOut"].MCPSet {
		t.Error("OptedOut method must report MCPSet=true so service default is overridden")
	}
	if byName["Inherited"].MCPScope != "chat session" {
		t.Errorf("MCPScope: got %q", byName["Inherited"].MCPScope)
	}
}

func TestGetXVarName_NilOptions(t *testing.T) {
	v := &descriptorpb.EnumValueDescriptorProto{Name: sp("TEST")}
	if got := getXVarName(v); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// Test options with empty MethodOptions (extension not set -> type assertion fails)
func TestGetRequiredHeaders_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getRequiredHeaders(m); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetQueryParamsTarget_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getQueryParamsTarget(m); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetExcludeAuth_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getExcludeAuth(m); got {
		t.Error("expected false")
	}
}

func TestGetAuthMethod_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getAuthMethod(m); got {
		t.Error("expected false")
	}
}

func TestGetFieldRequired_EmptyOptions(t *testing.T) {
	f := &descriptorpb.FieldDescriptorProto{
		Name:    sp("test"),
		Options: &descriptorpb.FieldOptions{},
	}
	if got := getFieldRequired(f); got {
		t.Error("expected false")
	}
}

func TestGetSSE_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getSSE(m); got {
		t.Error("expected false")
	}
}

func TestGetWSMode_EmptyOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	if got := getWSMode(m); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetDisplayName_EmptyOptions(t *testing.T) {
	s := &descriptorpb.ServiceDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.ServiceOptions{},
	}
	if got := getDisplayName(s); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetPathPrefix_EmptyOptions(t *testing.T) {
	s := &descriptorpb.ServiceDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.ServiceOptions{},
	}
	if got := getPathPrefix(s); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetXVarName_EmptyOptions(t *testing.T) {
	v := &descriptorpb.EnumValueDescriptorProto{
		Name:    sp("TEST"),
		Options: &descriptorpb.EnumValueOptions{},
	}
	if got := getXVarName(v); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// TestOptionGetters_NilOptions covers the early `if .Options == nil` branch
// of every option getter — the EmptyOptions tests above only exercise the
// type-assertion failure path.
func TestOptionGetters_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("M")}
	if got := getRequiredHeaders(m); got != nil {
		t.Errorf("getRequiredHeaders: got %v", got)
	}
	if got := getQueryParamsTarget(m); got != "" {
		t.Errorf("getQueryParamsTarget: got %q", got)
	}
	if getExcludeAuth(m) {
		t.Error("getExcludeAuth: got true")
	}
	if getAuthMethod(m) {
		t.Error("getAuthMethod: got true")
	}
	if getSSE(m) {
		t.Error("getSSE: got true")
	}
	if got := getWSMode(m); got != "" {
		t.Errorf("getWSMode: got %q", got)
	}

	f := &descriptorpb.FieldDescriptorProto{Name: sp("f")}
	if getFieldRequired(f) {
		t.Error("getFieldRequired: got true")
	}

	s := &descriptorpb.ServiceDescriptorProto{Name: sp("S")}
	if got := getDisplayName(s); got != "" {
		t.Errorf("getDisplayName: got %q", got)
	}
	if got := getPathPrefix(s); got != "" {
		t.Errorf("getPathPrefix: got %q", got)
	}
}

// --- Test collectNestedEnums with nested message containing enum ---

func TestParseNestedMessageWithNestedEnum(t *testing.T) {
	innerEnum := &descriptorpb.EnumDescriptorProto{
		Name: sp("InnerStatus"),
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: sp("INNER_STATUS_UNSPECIFIED"), Number: i32p(0)},
			{Name: sp("INNER_STATUS_OK"), Number: i32p(1)},
		},
	}
	innerMsg := &descriptorpb.DescriptorProto{
		Name:     sp("Inner"),
		EnumType: []*descriptorpb.EnumDescriptorProto{innerEnum},
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("status"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: sp(".test.v1.Outer.Inner.InnerStatus"),
			},
		},
	}
	outerMsg := &descriptorpb.DescriptorProto{
		Name:       sp("Outer"),
		NestedType: []*descriptorpb.DescriptorProto{innerMsg},
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     sp("inner"),
				Number:   i32p(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: sp(".test.v1.Outer.Inner"),
			},
		},
	}

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			outerMsg,
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Do"),
						InputType:  sp(".test.v1.Outer"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("GET", "/test"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	// Verify the outer message has an inner field
	m := api.Services[0].Methods[0]
	if len(m.InputType.Fields) != 1 {
		t.Fatalf("expected 1 field in Outer, got %d", len(m.InputType.Fields))
	}
	if m.InputType.Fields[0].Name != "inner" {
		t.Errorf("expected field 'inner', got %q", m.InputType.Fields[0].Name)
	}
}

// --- Test extractHTTPRule with PUT, DELETE, PATCH ---

func TestExtractHTTPRule_AllMethods(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/get"},
		{"POST", "/post"},
		{"PUT", "/put"},
		{"DELETE", "/delete"},
		{"PATCH", "/patch"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			opts := httpMethodOpts(tt.method, tt.path)
			m := &descriptorpb.MethodDescriptorProto{
				Name:    sp("Test"),
				Options: opts,
			}
			gotMethod, gotPath := extractHTTPRule(m)
			if gotMethod != tt.method {
				t.Errorf("method = %q, want %q", gotMethod, tt.method)
			}
			if gotPath != tt.path {
				t.Errorf("path = %q, want %q", gotPath, tt.path)
			}
		})
	}
}

func TestExtractHTTPRule_NilOptions(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{Name: sp("Test")}
	method, path := extractHTTPRule(m)
	if method != "" || path != "" {
		t.Errorf("expected empty for nil options, got %q, %q", method, path)
	}
}

func TestExtractHTTPRule_NoHTTPExtension(t *testing.T) {
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: &descriptorpb.MethodOptions{},
	}
	method, path := extractHTTPRule(m)
	if method != "" || path != "" {
		t.Errorf("expected empty for no HTTP ext, got %q, %q", method, path)
	}
}

func TestExtractHTTPRule_EmptyPattern(t *testing.T) {
	// HttpRule with no pattern set (Custom or nil pattern)
	opts := &descriptorpb.MethodOptions{}
	rule := &annotations.HttpRule{} // no pattern
	proto.SetExtension(opts, annotations.E_Http, rule)
	m := &descriptorpb.MethodDescriptorProto{
		Name:    sp("Test"),
		Options: opts,
	}
	method, path := extractHTTPRule(m)
	if method != "" || path != "" {
		t.Errorf("expected empty for nil pattern, got %q, %q", method, path)
	}
}

// Test resolveMessageType when the type is not in msgMap (stub path)
func TestParseUnresolvedMessageType(t *testing.T) {
	// Create a service that references a message type not defined in any file
	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			// Only define Resp, not Req - so Req won't be found in msgMap
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("Do"),
						InputType:  sp(".other.pkg.UnknownReq"),
						OutputType: sp(".test.v1.Resp"),
						Options:    httpMethodOpts("GET", "/test"),
					},
				},
			},
		},
	)

	api, err := Parse(req)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(api.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(api.Services))
	}
	m := api.Services[0].Methods[0]
	// Input type should be a stub with just the name
	if m.InputType.Name != "UnknownReq" {
		t.Errorf("InputType.Name = %q, want UnknownReq", m.InputType.Name)
	}
	if m.InputType.FullName != ".other.pkg.UnknownReq" {
		t.Errorf("InputType.FullName = %q, want .other.pkg.UnknownReq", m.InputType.FullName)
	}
}

// Test validateOneofUniqueness with output type error
// Test Parse returning validate error (SSE on unary -> validateStreamingOptions error)
func TestParseValidateError(t *testing.T) {
	opts := httpMethodOpts("GET", "/events")
	opts = withSSE(opts) // SSE on a unary method = validation error

	req := makeRequest("test.v1", "test.proto",
		[]*descriptorpb.DescriptorProto{
			makeSimpleMessage("Req"),
			makeSimpleMessage("Resp"),
		},
		nil,
		[]*descriptorpb.ServiceDescriptorProto{
			{
				Name: sp("Svc"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       sp("BadSSE"),
						InputType:  sp(".test.v1.Req"),
						OutputType: sp(".test.v1.Resp"),
						Options:    opts,
						// unary (no streaming flags) + SSE = error
					},
				},
			},
		},
	)

	_, err := Parse(req)
	if err == nil {
		t.Fatal("expected validation error for SSE on unary")
	}
	if !contains(err.Error(), "sse") {
		t.Errorf("error should mention sse, got: %v", err)
	}
}

func TestValidateOneofUniqueness_OutputTypeConflict(t *testing.T) {
	type1 := &MessageType{
		Name:     "In",
		FullName: ".test.v1.In",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "text", IsMessage: true, MessageName: "SharedMsg"}}},
		},
	}
	type2 := &MessageType{
		Name:     "Out",
		FullName: ".test.v1.Out",
		OneofDecls: []*OneofDecl{
			{Name: "data", Variants: []*OneofVariant{{FieldName: "shared", IsMessage: true, MessageName: "SharedMsg"}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: type1, OutputType: type2},
				},
			},
		},
	}

	err := validateOneofUniqueness(api, nil)
	if err == nil {
		t.Fatal("expected error for oneof uniqueness conflict via output type")
	}
}

func TestExtractGoPackage_WithAlias(t *testing.T) {
	opts := &descriptorpb.FileOptions{}
	pkg := "github.com/foo/bar;barpb"
	opts.GoPackage = &pkg
	file := &descriptorpb.FileDescriptorProto{Options: opts}

	got := extractGoPackage(file)
	if got != "github.com/foo/bar" {
		t.Errorf("extractGoPackage() = %q, want github.com/foo/bar", got)
	}
}

func TestExtractGoPackage_WithoutAlias(t *testing.T) {
	opts := &descriptorpb.FileOptions{}
	pkg := "github.com/foo/bar"
	opts.GoPackage = &pkg
	file := &descriptorpb.FileDescriptorProto{Options: opts}

	got := extractGoPackage(file)
	if got != "github.com/foo/bar" {
		t.Errorf("extractGoPackage() = %q, want github.com/foo/bar", got)
	}
}

func TestExtractGoPackage_Empty(t *testing.T) {
	file := &descriptorpb.FileDescriptorProto{}
	got := extractGoPackage(file)
	if got != "" {
		t.Errorf("extractGoPackage() = %q, want empty", got)
	}
}
