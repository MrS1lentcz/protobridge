package parser

import "google.golang.org/protobuf/types/descriptorpb"

// ParsedAPI is the complete parsed representation of all proto files
// processed by protobridge. It is the sole input to the code generator.
type ParsedAPI struct {
	Services   []*Service
	AuthMethod *AuthMethod

	// Messages indexes every message type reachable from the processed
	// proto files by its fully-qualified name (with leading dot, matching
	// proto descriptor convention — e.g. ".taskboard.v1.Task"). Nested
	// message fields reference peers by TypeName; consumers look up the
	// full MessageType here to inline a schema recursively.
	Messages map[string]*MessageType

	// Events lists every message annotated with (protobridge.event), in
	// proto declaration order (stable across runs). Consumed by
	// protoc-gen-events-go to emit typed Emit*/Subscribe* helpers and the
	// AsyncAPI document.
	Events []*Event

	// BroadcastServices lists every gRPC service annotated with
	// (protobridge.broadcast). Each entry declares one WebSocket endpoint:
	// REST plugin emits the handler at the route, subscribes to every
	// listed event's bus subject, and translates incoming messages to
	// the typed envelope (the oneof'd return type of the service's only
	// streaming RPC). Backend leaves the RPC unimplemented.
	BroadcastServices []*BroadcastService
}

// BroadcastService is one (protobridge.broadcast) service declaration —
// metadata only, no backend code is expected to implement the underlying
// gRPC method.
type BroadcastService struct {
	Name        string             // service name, e.g. "OrderBroadcast"
	Route       string             // HTTP path the WS endpoint is mounted at
	GoPackage   string             // Go import path of the service's proto package
	ProtoPackage string            // proto package, e.g. "myapp.events"
	Envelope    *MessageType       // return type of the streaming RPC; the oneof envelope
	Events      []*BroadcastEvent  // one entry per oneof variant in Envelope
}

// BroadcastEvent links a oneof variant in the envelope to its underlying
// (protobridge.event)-annotated message. Subject, Visibility and GoPackage
// are copied from the matching Event for codegen convenience — GoPackage
// lets the per-service marshaler import event messages from multiple proto
// packages via aliased imports.
type BroadcastEvent struct {
	OneofFieldName string       // snake_case of the oneof field, e.g. "order_created"
	Message        *MessageType // the event's proto message type
	Subject        string       // bus subject (resolved from (protobridge.event))
	Visibility     EventVisibility
	GoPackage      string // Go import path of the event message's proto package
}

// Event is a message-level annotation that turns a proto message into a
// pub/sub event. The fields mirror the EventOptions extension defined in
// proto/protobridge/events.proto, with subject resolved (annotation value
// or snake_case of the message name when blank).
type Event struct {
	Message      *MessageType
	Subject      string // resolved: explicit subject or snake_case of Message.Name
	Kind         EventKind
	DurableGroup string
	Visibility   EventVisibility
	Description  string

	// GoPackage is the Go import path of the proto package that owns this
	// message, copied so the events plugin doesn't have to re-derive it.
	GoPackage string
}

// EventKind mirrors the protobridge.EventKind proto enum. Kept as a small
// integer alias so generator code can use named constants without importing
// the runtime events package (which would create a parser → runtime cycle).
type EventKind int

const (
	EventKindUnspecified EventKind = iota
	EventKindBroadcast
	EventKindDurable
	EventKindBoth
)

// EventVisibility mirrors the protobridge.Visibility proto enum.
type EventVisibility int

const (
	EventVisibilityUnspecified EventVisibility = iota
	EventVisibilityPublic
	EventVisibilityInternal
)

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
