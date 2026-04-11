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
	val := proto.GetExtension(m.Options, optionspb.E_QueryParamsTarget)
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

func getExcludeAuth(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	val := proto.GetExtension(m.Options, optionspb.E_ExcludeAuth)
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

func getAuthMethod(m *descriptorpb.MethodDescriptorProto) bool {
	if m.Options == nil {
		return false
	}
	val := proto.GetExtension(m.Options, optionspb.E_AuthMethod)
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

func getFieldRequired(f *descriptorpb.FieldDescriptorProto) bool {
	if f.Options == nil {
		return false
	}
	val := proto.GetExtension(f.Options, optionspb.E_Required)
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

func getDisplayName(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	val := proto.GetExtension(s.Options, optionspb.E_DisplayName)
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

func getPathPrefix(s *descriptorpb.ServiceDescriptorProto) string {
	if s.Options == nil {
		return ""
	}
	val := proto.GetExtension(s.Options, optionspb.E_PathPrefix)
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

func getXVarName(v *descriptorpb.EnumValueDescriptorProto) string {
	if v.Options == nil {
		return ""
	}
	val := proto.GetExtension(v.Options, optionspb.E_XVarName)
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}
