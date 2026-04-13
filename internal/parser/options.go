package parser

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// Each getter follows the same shape: bail when Options is nil, otherwise
// pull the typed extension value. proto.GetExtension returns the registered
// extension type's zero value for unset extensions, so for typed extensions
// the assertion always succeeds — no `, ok` defensive branch is needed.

func getRequiredHeaders(m *descriptorpb.MethodDescriptorProto) []string {
	if m.Options == nil {
		return nil
	}
	val := proto.GetExtension(m.Options, optionspb.E_RequiredHeaders)
	if headers, ok := val.([]string); ok && len(headers) > 0 {
		return headers
	}
	return nil
}

func getQueryParamsTarget(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	return proto.GetExtension(m.Options, optionspb.E_QueryParamsTarget).(string)
}

func getExcludeAuth(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	return proto.GetExtension(m.Options, optionspb.E_ExcludeAuth).(bool)
}

func getAuthMethod(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	return proto.GetExtension(m.Options, optionspb.E_AuthMethod).(bool)
}

func getFieldRequired(f *descriptorpb.FieldDescriptorProto) bool {
	if f.Options == nil {
		return false
	}
	return proto.GetExtension(f.Options, optionspb.E_Required).(bool)
}

func getSSE(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	return proto.GetExtension(m.Options, optionspb.E_Sse).(bool)
}

func getWSMode(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	return proto.GetExtension(m.Options, optionspb.E_WsMode).(string)
}

func getDisplayName(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	return proto.GetExtension(s.Options, optionspb.E_DisplayName).(string)
}

func getPathPrefix(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	return proto.GetExtension(s.Options, optionspb.E_PathPrefix).(string)
}

// getMCP returns (value, set). The "set" flag is needed to detect explicit
// per-method opt-out (`(protobridge.mcp) = false`) when service-level
// `mcp_default = true` is in effect.
func getMCP(m *descriptorpb.MethodDescriptorProto) (val, set bool) {
	if m.Options == nil || !proto.HasExtension(m.Options, optionspb.E_Mcp) {
		return false, false
	}
	return proto.GetExtension(m.Options, optionspb.E_Mcp).(bool), true
}

func getMCPScope(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	return proto.GetExtension(m.Options, optionspb.E_McpScope).(string)
}

func getMCPDescription(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	return proto.GetExtension(m.Options, optionspb.E_McpDescription).(string)
}

func getMCPDefault(s *descriptorpb.ServiceDescriptorProto) bool {
	if s.Options == nil {
		return false
	}
	return proto.GetExtension(s.Options, optionspb.E_McpDefault).(bool)
}

func getXVarName(v *descriptorpb.EnumValueDescriptorProto) string {
	if v.Options == nil {
		return ""
	}
	return proto.GetExtension(v.Options, optionspb.E_XVarName).(string)
}

// getEventOptions returns the (protobridge.event) MessageOptions extension
// when present, plus a flag indicating presence (so callers can distinguish
// "not annotated" from "annotated with all-default values"). Once
// HasExtension returns true the type assertion is guaranteed to succeed
// for typed extensions, so no defensive `, ok` branch is needed.
func getEventOptions(m *descriptorpb.DescriptorProto) (*optionspb.EventOptions, bool) {
	if m.Options == nil || !proto.HasExtension(m.Options, optionspb.E_Event) {
		return nil, false
	}
	return proto.GetExtension(m.Options, optionspb.E_Event).(*optionspb.EventOptions), true
}
