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
