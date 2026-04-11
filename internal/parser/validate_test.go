package parser

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

func buildMsgMap(msgs ...*descriptorpb.DescriptorProto) map[string]*descriptorpb.DescriptorProto {
	m := make(map[string]*descriptorpb.DescriptorProto)
	for _, msg := range msgs {
		m[".test.v1."+msg.GetName()] = msg
	}
	return m
}

func TestValidateOneofUniqueness_SameMessageDedup(t *testing.T) {
	// Same message type used in two different methods should be OK (dedup).
	inputType := &MessageType{
		Name:     "Req",
		FullName: ".test.v1.Req",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "text", IsMessage: true, MessageName: "TextPayload"},
				},
			},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: inputType},
					{Name: "B", InputType: inputType}, // same pointer, same FullName
				},
			},
		},
	}

	if err := validateOneofUniqueness(api, nil); err != nil {
		t.Errorf("expected no error for same message reuse, got: %v", err)
	}
}

func TestValidateOneofUniqueness_Conflict(t *testing.T) {
	type1 := &MessageType{
		Name:     "Req1",
		FullName: ".test.v1.Req1",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "text", IsMessage: true, MessageName: "SharedMsg"},
				},
			},
		},
	}
	type2 := &MessageType{
		Name:     "Req2",
		FullName: ".test.v1.Req2",
		OneofDecls: []*OneofDecl{
			{
				Name: "data",
				Variants: []*OneofVariant{
					{FieldName: "shared", IsMessage: true, MessageName: "SharedMsg"},
				},
			},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: type1},
					{Name: "B", InputType: type2},
				},
			},
		},
	}

	err := validateOneofUniqueness(api, nil)
	if err == nil {
		t.Fatal("expected error for oneof uniqueness conflict")
	}
	if !strings.Contains(err.Error(), "SharedMsg") {
		t.Errorf("error should mention SharedMsg, got: %v", err)
	}
}

func TestValidateOneofInputConstraint_VariantUsedAsInput(t *testing.T) {
	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"},
				},
			},
		},
	}
	childInputType := &MessageType{
		Name:     "ChildMsg",
		FullName: ".test.v1.ChildMsg",
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: parentType, OutputType: &MessageType{Name: "Resp", FullName: ".test.v1.Resp"}},
					{Name: "B", InputType: childInputType, OutputType: &MessageType{Name: "Resp2", FullName: ".test.v1.Resp2"}},
				},
			},
		},
	}

	err := validateOneofInputConstraint(api)
	if err == nil {
		t.Fatal("expected error when oneof variant is used as RPC input")
	}
	if !strings.Contains(err.Error(), "ChildMsg") {
		t.Errorf("error should mention ChildMsg, got: %v", err)
	}
}

func TestValidateOneofInputConstraint_OK(t *testing.T) {
	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"},
				},
			},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: parentType, OutputType: &MessageType{Name: "Resp", FullName: ".test.v1.Resp"}},
				},
			},
		},
	}

	if err := validateOneofInputConstraint(api); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateOneofDiscriminatorField_ReservedName(t *testing.T) {
	childMsg := &descriptorpb.DescriptorProto{
		Name: sp("ChildMsg"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("protobridge_disc"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(childMsg)

	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"},
				},
			},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: parentType},
				},
			},
		},
	}

	err := validateOneofDiscriminatorField(api, msgMap)
	if err == nil {
		t.Fatal("expected error for reserved field name protobridge_disc")
	}
	if !strings.Contains(err.Error(), "protobridge_disc") {
		t.Errorf("error should mention protobridge_disc, got: %v", err)
	}
}

func TestValidateOneofDiscriminatorField_OK(t *testing.T) {
	childMsg := &descriptorpb.DescriptorProto{
		Name: sp("ChildMsg"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("text"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(childMsg)

	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{
				Name: "payload",
				Variants: []*OneofVariant{
					{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"},
				},
			},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "A", InputType: parentType},
				},
			},
		},
	}

	if err := validateOneofDiscriminatorField(api, msgMap); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStreamingOptions_SSEOnNonServerStreaming(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "Bad", SSE: true, StreamType: StreamUnary},
				},
			},
		},
	}

	err := validateStreamingOptions(api)
	if err == nil {
		t.Fatal("expected error for SSE on unary")
	}
	if !strings.Contains(err.Error(), "sse") {
		t.Errorf("error should mention sse, got: %v", err)
	}
}

func TestValidateStreamingOptions_SSEOnServerStreamingOK(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "Good", SSE: true, StreamType: StreamServer},
				},
			},
		},
	}

	if err := validateStreamingOptions(api); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStreamingOptions_WSModeOnUnary(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "Bad", WSMode: "private", StreamType: StreamUnary},
				},
			},
		},
	}

	err := validateStreamingOptions(api)
	if err == nil {
		t.Fatal("expected error for ws_mode on unary")
	}
	if !strings.Contains(err.Error(), "ws_mode") {
		t.Errorf("error should mention ws_mode, got: %v", err)
	}
}

func TestValidateStreamingOptions_InvalidWSMode(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{Name: "Bad", WSMode: "invalid", StreamType: StreamServer},
				},
			},
		},
	}

	err := validateStreamingOptions(api)
	if err == nil {
		t.Fatal("expected error for invalid ws_mode")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention 'invalid', got: %v", err)
	}
}

func TestValidateStreamingOptions_ValidWSMode(t *testing.T) {
	for _, mode := range []string{"private", "broadcast"} {
		t.Run(mode, func(t *testing.T) {
			api := &ParsedAPI{
				Services: []*Service{
					{
						Name: "Svc",
						Methods: []*Method{
							{Name: "Ok", WSMode: mode, StreamType: StreamBidi},
						},
					},
				},
			}
			if err := validateStreamingOptions(api); err != nil {
				t.Errorf("expected no error for ws_mode=%q, got: %v", mode, err)
			}
		})
	}
}

func TestValidateQueryParamsTarget_InvalidTarget(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{
						Name:              "List",
						QueryParamsTarget: "nonexistent",
						InputType: &MessageType{
							Name:     "Req",
							FullName: ".test.v1.Req",
							Fields: []*Field{
								{Name: "name", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}

	err := validateQueryParamsTarget(api, nil)
	if err == nil {
		t.Fatal("expected error for invalid query_params_target")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention 'nonexistent', got: %v", err)
	}
}

func TestValidateQueryParamsTarget_StringFieldNotValid(t *testing.T) {
	// query_params_target must reference a message-typed field, not a string.
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{
						Name:              "List",
						QueryParamsTarget: "name",
						InputType: &MessageType{
							Name:     "Req",
							FullName: ".test.v1.Req",
							Fields: []*Field{
								{Name: "name", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
							},
						},
					},
				},
			},
		},
	}

	err := validateQueryParamsTarget(api, nil)
	if err == nil {
		t.Fatal("expected error: string field should not be valid for query_params_target")
	}
}

func TestValidateQueryParamsTarget_ValidTarget(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{
						Name:              "List",
						QueryParamsTarget: "filter",
						InputType: &MessageType{
							Name:     "Req",
							FullName: ".test.v1.Req",
							Fields: []*Field{
								{Name: "filter", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".test.v1.Filter"},
							},
						},
					},
				},
			},
		},
	}

	if err := validateQueryParamsTarget(api, nil); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateQueryParamsTarget_EmptyTargetSkipped(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{
						Name:              "List",
						QueryParamsTarget: "",
						InputType:         &MessageType{Name: "Req", FullName: ".test.v1.Req"},
					},
				},
			},
		},
	}

	if err := validateQueryParamsTarget(api, nil); err != nil {
		t.Errorf("expected no error for empty target, got: %v", err)
	}
}

func TestValidateAuthMethod_MissingMapField(t *testing.T) {
	inputMsg := &descriptorpb.DescriptorProto{
		Name: sp("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("token"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(inputMsg)

	api := &ParsedAPI{
		AuthMethod: &AuthMethod{
			ServiceName: "AuthSvc",
			MethodName:  "Auth",
			InputType: &MessageType{
				Name:     "AuthReq",
				FullName: ".test.v1.AuthReq",
				Fields: []*Field{
					{Name: "token", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
				},
			},
		},
	}

	err := validateAuthMethod(api, msgMap)
	if err == nil {
		t.Fatal("expected error for missing map field in auth method input")
	}
	if !strings.Contains(err.Error(), "map<string,string>") {
		t.Errorf("error should mention map<string,string>, got: %v", err)
	}
}

func TestValidateAuthMethod_WithMapFieldOK(t *testing.T) {
	mapEntry := &descriptorpb.DescriptorProto{
		Name: sp("HeadersEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("key"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: sp("value"), Number: i32p(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
		Options: &descriptorpb.MessageOptions{MapEntry: bp(true)},
	}
	msgMap := map[string]*descriptorpb.DescriptorProto{
		".test.v1.AuthReq":              {Name: sp("AuthReq")},
		".test.v1.AuthReq.HeadersEntry": mapEntry,
	}

	api := &ParsedAPI{
		AuthMethod: &AuthMethod{
			ServiceName: "AuthSvc",
			MethodName:  "Auth",
			InputType: &MessageType{
				Name:     "AuthReq",
				FullName: ".test.v1.AuthReq",
				Fields: []*Field{
					{
						Name:     "headers",
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
						TypeName: ".test.v1.AuthReq.HeadersEntry",
					},
				},
			},
		},
	}

	if err := validateAuthMethod(api, msgMap); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateAuthMethod_NilAuthMethod(t *testing.T) {
	api := &ParsedAPI{}
	if err := validateAuthMethod(api, nil); err != nil {
		t.Errorf("expected no error for nil auth method, got: %v", err)
	}
}

func TestValidateFullPipeline(t *testing.T) {
	// Test the full validate function with a valid API.
	api := &ParsedAPI{
		Services: []*Service{
			{
				Name: "Svc",
				Methods: []*Method{
					{
						Name:       "Do",
						StreamType: StreamUnary,
						InputType:  &MessageType{Name: "Req", FullName: ".test.v1.Req"},
						OutputType: &MessageType{Name: "Resp", FullName: ".test.v1.Resp"},
					},
				},
			},
		},
	}

	if err := validate(api, nil); err != nil {
		t.Errorf("expected no error for valid API, got: %v", err)
	}
}

// Test validate returns error from validateOneofUniqueness
func TestValidate_OneofUniquenessError(t *testing.T) {
	type1 := &MessageType{
		Name:     "Req1",
		FullName: ".test.v1.Req1",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "text", IsMessage: true, MessageName: "SharedMsg"}}},
		},
	}
	type2 := &MessageType{
		Name:     "Req2",
		FullName: ".test.v1.Req2",
		OneofDecls: []*OneofDecl{
			{Name: "data", Variants: []*OneofVariant{{FieldName: "shared", IsMessage: true, MessageName: "SharedMsg"}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: type1},
				{Name: "B", InputType: type2},
			}},
		},
	}

	err := validate(api, nil)
	if err == nil {
		t.Fatal("expected error from validate")
	}
	if !strings.Contains(err.Error(), "SharedMsg") {
		t.Errorf("error should mention SharedMsg, got: %v", err)
	}
}

// Test validate returns error from validateOneofInputConstraint
func TestValidate_OneofInputConstraintError(t *testing.T) {
	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"}}},
		},
	}
	childInputType := &MessageType{Name: "ChildMsg", FullName: ".test.v1.ChildMsg"}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: parentType, OutputType: &MessageType{Name: "Resp", FullName: ".test.v1.Resp"}},
				{Name: "B", InputType: childInputType, OutputType: &MessageType{Name: "Resp2", FullName: ".test.v1.Resp2"}},
			}},
		},
	}

	err := validate(api, nil)
	if err == nil {
		t.Fatal("expected error from validate")
	}
}

// Test validate returns error from validateOneofDiscriminatorField
func TestValidate_OneofDiscriminatorFieldError(t *testing.T) {
	childMsg := &descriptorpb.DescriptorProto{
		Name: sp("ChildMsg"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("protobridge_disc"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(childMsg)

	parentType := &MessageType{
		Name:     "Parent",
		FullName: ".test.v1.Parent",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: parentType},
			}},
		},
	}

	err := validate(api, msgMap)
	if err == nil {
		t.Fatal("expected error from validate")
	}
}

// Test validate returns error from validateStreamingOptions
func TestValidate_StreamingOptionsError(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "Bad", SSE: true, StreamType: StreamUnary},
			}},
		},
	}

	err := validate(api, nil)
	if err == nil {
		t.Fatal("expected error from validate for SSE on unary")
	}
}

// Test validate returns error from validateQueryParamsTarget
func TestValidate_QueryParamsTargetError(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{
					Name:              "List",
					QueryParamsTarget: "nonexistent",
					InputType: &MessageType{
						Name:     "Req",
						FullName: ".test.v1.Req",
						Fields:   []*Field{{Name: "name", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING}},
					},
				},
			}},
		},
	}

	err := validate(api, nil)
	if err == nil {
		t.Fatal("expected error from validate for invalid query_params_target")
	}
}

// Test validate returns error from validateAuthMethod
func TestValidate_AuthMethodError(t *testing.T) {
	inputMsg := &descriptorpb.DescriptorProto{
		Name: sp("AuthReq"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("token"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(inputMsg)

	api := &ParsedAPI{
		AuthMethod: &AuthMethod{
			ServiceName: "AuthSvc",
			MethodName:  "Auth",
			InputType: &MessageType{
				Name:     "AuthReq",
				FullName: ".test.v1.AuthReq",
				Fields:   []*Field{{Name: "token", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING}},
			},
		},
	}

	err := validate(api, msgMap)
	if err == nil {
		t.Fatal("expected error from validate for missing map field")
	}
}

// Test validateAuthMethod with nil InputType
func TestValidateAuthMethod_NilInputType(t *testing.T) {
	api := &ParsedAPI{
		AuthMethod: &AuthMethod{
			ServiceName: "AuthSvc",
			MethodName:  "Auth",
			InputType:   nil,
		},
	}

	err := validateAuthMethod(api, nil)
	if err == nil {
		t.Fatal("expected error for nil InputType")
	}
	if !strings.Contains(err.Error(), "unresolvable") {
		t.Errorf("error should mention 'unresolvable', got: %v", err)
	}
}

// Test validateOneofUniqueness with output types
func TestValidateOneofUniqueness_OutputType(t *testing.T) {
	outputType := &MessageType{
		Name:     "Resp",
		FullName: ".test.v1.Resp",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "text", IsMessage: true, MessageName: "SharedMsg"}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", OutputType: outputType},
			}},
		},
	}

	if err := validateOneofUniqueness(api, nil); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// Test validateOneofDiscriminatorField with output types
func TestValidateOneofDiscriminatorField_OutputType(t *testing.T) {
	childMsg := &descriptorpb.DescriptorProto{
		Name: sp("ChildMsg"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: sp("protobridge_disc"), Number: i32p(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}
	msgMap := buildMsgMap(childMsg)

	outputType := &MessageType{
		Name:     "Resp",
		FullName: ".test.v1.Resp",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "child", IsMessage: true, MessageName: "ChildMsg"}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", OutputType: outputType},
			}},
		},
	}

	err := validateOneofDiscriminatorField(api, msgMap)
	if err == nil {
		t.Fatal("expected error for reserved field name in output type")
	}
}

// Test validateOneofInputConstraint with nil InputType
func TestValidateOneofInputConstraint_NilInputType(t *testing.T) {
	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: nil, OutputType: &MessageType{Name: "Resp", FullName: ".test.v1.Resp"}},
			}},
		},
	}

	if err := validateOneofInputConstraint(api); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// Test validateOneofUniqueness with non-message variants (should skip)
func TestValidateOneofUniqueness_NonMessageVariant(t *testing.T) {
	inputType := &MessageType{
		Name:     "Req",
		FullName: ".test.v1.Req",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "text", IsMessage: false}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: inputType},
			}},
		},
	}

	if err := validateOneofUniqueness(api, nil); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// Test validateOneofDiscriminatorField with non-message variant (should skip)
func TestValidateOneofDiscriminatorField_NonMessageVariant(t *testing.T) {
	inputType := &MessageType{
		Name:     "Req",
		FullName: ".test.v1.Req",
		OneofDecls: []*OneofDecl{
			{Name: "payload", Variants: []*OneofVariant{{FieldName: "text", IsMessage: false}}},
		},
	}

	api := &ParsedAPI{
		Services: []*Service{
			{Name: "Svc", Methods: []*Method{
				{Name: "A", InputType: inputType},
			}},
		},
	}

	if err := validateOneofDiscriminatorField(api, nil); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}
