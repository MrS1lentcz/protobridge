package parser

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

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
	val, ok := proto.GetExtension(m.Options, optionspb.E_QueryParamsTarget).(string)
	if !ok {
		return ""
	}
	return val
}

func getExcludeAuth(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	val, ok := proto.GetExtension(m.Options, optionspb.E_ExcludeAuth).(bool)
	if !ok {
		return false
	}
	return val
}

func getAuthMethod(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	val, ok := proto.GetExtension(m.Options, optionspb.E_AuthMethod).(bool)
	if !ok {
		return false
	}
	return val
}

func getFieldRequired(f *descriptorpb.FieldDescriptorProto) bool {
	if f.Options == nil {
		return false
	}
	val, ok := proto.GetExtension(f.Options, optionspb.E_Required).(bool)
	if !ok {
		return false
	}
	return val
}

func getSSE(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	val, ok := proto.GetExtension(m.Options, optionspb.E_Sse).(bool)
	if !ok {
		return false
	}
	return val
}

func getWSMode(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	val, ok := proto.GetExtension(m.Options, optionspb.E_WsMode).(string)
	if !ok {
		return ""
	}
	return val
}

func getDisplayName(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	val, ok := proto.GetExtension(s.Options, optionspb.E_DisplayName).(string)
	if !ok {
		return ""
	}
	return val
}

func getPathPrefix(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	val, ok := proto.GetExtension(s.Options, optionspb.E_PathPrefix).(string)
	if !ok {
		return ""
	}
	return val
}

// getMCP returns (value, set). The "set" flag is needed to detect explicit
// per-method opt-out (`(protobridge.mcp) = false`) when service-level
// `mcp_default = true` is in effect.
func getMCP(m *descriptorpb.MethodDescriptorProto) (val, set bool) {
	if m.Options == nil || !proto.HasExtension(m.Options, optionspb.E_Mcp) {
		return false, false
	}
	v, ok := proto.GetExtension(m.Options, optionspb.E_Mcp).(bool)
	if !ok {
		return false, false
	}
	return v, true
}

func getMCPScope(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	v, _ := proto.GetExtension(m.Options, optionspb.E_McpScope).(string)
	return v
}

func getMCPDescription(m *descriptorpb.MethodDescriptorProto) string {
	if m.Options == nil {
		return ""
	}
	v, _ := proto.GetExtension(m.Options, optionspb.E_McpDescription).(string)
	return v
}

func getMCPDefault(s *descriptorpb.ServiceDescriptorProto) bool {
	if s.Options == nil {
		return false
	}
	v, _ := proto.GetExtension(s.Options, optionspb.E_McpDefault).(bool)
	return v
}

// getEventOptions returns the (protobridge.event) MessageOptions extension
// when present, plus a flag indicating presence (so callers can distinguish
// "not annotated" from "annotated with all-default values").
func getEventOptions(m *descriptorpb.DescriptorProto) (*optionspb.EventOptions, bool) {
	if m.Options == nil || !proto.HasExtension(m.Options, optionspb.E_Event) {
		return nil, false
	}
	v, ok := proto.GetExtension(m.Options, optionspb.E_Event).(*optionspb.EventOptions)
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

func getXVarName(v *descriptorpb.EnumValueDescriptorProto) string {
	if v.Options == nil {
		return ""
	}
	val, ok := proto.GetExtension(v.Options, optionspb.E_XVarName).(string)
	if !ok {
		return ""
	}
	return val
}
