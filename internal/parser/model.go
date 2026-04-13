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
	ProtoPackage string // proto package, e.g. "taskboard.v1"
	GoPackage    string // Go import path from go_package option, e.g. "github.com/foo/gen/taskboard/v1"
	DisplayName  string // from (protobridge.display_name), used as OpenAPI tag
	PathPrefix   string // from (protobridge.path_prefix), prepended to all HTTP paths
	MCPDefault   bool   // from (protobridge.mcp_default), service-wide MCP opt-in
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
	SSE              bool   // use SSE instead of WS (server-streaming only)
	WSMode           string // "private" or "broadcast" (streaming only)

	// MCP attributes — used by the MCP plugin to decide which RPCs become
	// tools and how they're presented to the LLM client.
	MCP            bool   // explicitly set or inherited from service mcp_default
	MCPSet         bool   // true if (protobridge.mcp) was set on this method (used to detect explicit opt-out)
	MCPScope       string // free-form hint, e.g. "chat session", appended to tool description
	MCPDescription string // override for the tool description; empty falls back to LeadingComment
	LeadingComment string // proto leading comment from SourceCodeInfo, used as default MCP tool description
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
	GoPackage   string // Go import path of the service's proto package
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
