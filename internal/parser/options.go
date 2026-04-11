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

func getXVarName(v *descriptorpb.EnumValueDescriptorProto) string {
	if v.Options == nil {
		return ""
	}
	return proto.GetExtension(v.Options, optionspb.E_XVarName).(string)
}
