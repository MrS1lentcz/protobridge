package parser

import "google.golang.org/protobuf/types/descriptorpb"

// ParsedAPI is the complete parsed representation of all proto files
// processed by protobridge. It is the sole input to the code generator.
type ParsedAPI struct {
	Services   []*Service
	AuthMethod *AuthMethod
}

type Service struct {
	Name         string // e.g. "VoiceChatService"
	ProtoPackage string
	DisplayName  string // from (protobridge.display_name), used as OpenAPI tag
	PathPrefix   string // from (protobridge.path_prefix), prepended to all HTTP paths
	Methods      []*Method
}

type Method struct {
	Name             string // e.g. "CreateVoiceChat"
	InputType        *MessageType
	OutputType       *MessageType
	HTTPMethod       string // GET, POST, PUT, DELETE, PATCH
	HTTPPath         string // e.g. "/chats/{chat_id}/message/voice"
	PathParams       []string
	RequiredHeaders  []string
	QueryParamsTarget string
	ExcludeAuth      bool
	StreamType       StreamType
}

type StreamType int

const (
	StreamUnary  StreamType = iota
	StreamServer
	StreamClient
	StreamBidi
)

type AuthMethod struct {
	ServiceName string
	MethodName  string
	InputType   *MessageType
	OutputType  *MessageType
}

type MessageType struct {
	Name       string // unqualified name, e.g. "AddVoiceChatMessageRequest"
	FullName   string // fully qualified, e.g. ".assistant_api.AddVoiceChatMessageRequest"
	Fields     []*Field
	OneofDecls []*OneofDecl
}

type Field struct {
	Name       string
	Number     int32
	Type       descriptorpb.FieldDescriptorProto_Type
	TypeName   string // for message/enum types
	Required   bool
	OneofIndex *int32 // nil if not part of a oneof
	Repeated   bool
	MapEntry   bool
	EnumValues []*EnumValue // populated for enum fields (excludes 0-value member)
}

type OneofDecl struct {
	Name     string
	Variants []*OneofVariant
}

type OneofVariant struct {
	FieldName   string
	IsMessage   bool
	MessageName string // unqualified message name (for discriminator)
}

type EnumValue struct {
	Name    string
	Number  int32
	XVarName string // from x_var_name option
}
