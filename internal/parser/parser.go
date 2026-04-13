package parser

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
)

// Parse processes a CodeGeneratorRequest and returns a structured ParsedAPI.
func Parse(req *pluginpb.CodeGeneratorRequest) (*ParsedAPI, error) {
	// Build a map of all message types by their fully qualified name.
	msgMap := make(map[string]*descriptorpb.DescriptorProto)
	// Build a map of all enum types by their fully qualified name.
	enumMap := make(map[string]*descriptorpb.EnumDescriptorProto)
	for _, file := range req.ProtoFile {
		pkg := file.GetPackage()
		for _, msg := range file.MessageType {
			fqn := "." + pkg + "." + msg.GetName()
			msgMap[fqn] = msg
			collectNestedMessages(msgMap, fqn, msg)
			collectNestedEnums(enumMap, fqn, msg)
		}
		for _, enum := range file.EnumType {
			fqn := "." + pkg + "." + enum.GetName()
			enumMap[fqn] = enum
		}
	}

	api := &ParsedAPI{}

	// Only process files that were explicitly requested for generation.
	filesToGenerate := make(map[string]bool)
	for _, name := range req.FileToGenerate {
		filesToGenerate[name] = true
	}

	for _, file := range req.ProtoFile {
		if !filesToGenerate[file.GetName()] {
			continue
		}

		goPackage := extractGoPackage(file)
		comments := buildLeadingComments(file)

		for svcIdx, svc := range file.Service {
			pathPrefix := getPathPrefix(svc)
			mcpDefault := getMCPDefault(svc)
			service := &Service{
				Name:         svc.GetName(),
				ProtoPackage: file.GetPackage(),
				GoPackage:    goPackage,
				DisplayName:  getDisplayName(svc),
				PathPrefix:   pathPrefix,
				MCPDefault:   mcpDefault,
			}

			for methodIdx, m := range svc.Method {
				isAuth := getAuthMethod(m)
				mcpVal, mcpSet := getMCP(m)
				mcpEnabled := mcpVal
				if !mcpSet {
					mcpEnabled = mcpDefault
				}
				if isAuth {
					if api.AuthMethod != nil {
						return nil, fmt.Errorf("multiple auth_method annotations found: %s.%s and %s.%s",
							api.AuthMethod.ServiceName, api.AuthMethod.MethodName,
							svc.GetName(), m.GetName())
					}
					api.AuthMethod = &AuthMethod{
						ServiceName: svc.GetName(),
						MethodName:  m.GetName(),
						GoPackage:   goPackage,
						InputType:   resolveMessageType(msgMap, enumMap, m.GetInputType()),
						OutputType:  resolveMessageType(msgMap, enumMap, m.GetOutputType()),
					}
				}

				httpMethod, httpPath := extractHTTPRule(m)
				// Methods without an HTTP annotation AND without MCP opt-in
				// are not exposed by any proxy — skip outright. Methods with
				// MCP=true but no HTTP rule still need to land in the model
				// so protoc-gen-mcp can see them; HTTPMethod stays empty
				// and the REST plugin filters on that.
				if httpMethod == "" && !mcpEnabled {
					continue
				}

				// Apply service-level path prefix when there is a path.
				fullPath := ""
				if httpPath != "" {
					fullPath = pathPrefix + httpPath
				}

				method := &Method{
					Name:              m.GetName(),
					InputType:         resolveMessageType(msgMap, enumMap, m.GetInputType()),
					OutputType:        resolveMessageType(msgMap, enumMap, m.GetOutputType()),
					HTTPMethod:        httpMethod,
					HTTPPath:          fullPath,
					PathParams:        extractPathParams(fullPath),
					RequiredHeaders:   getRequiredHeaders(m),
					QueryParamsTarget: getQueryParamsTarget(m),
					// Auth methods exposed as REST must skip the auth middleware
					// (otherwise login itself would require a prior login).
					ExcludeAuth:       getExcludeAuth(m) || isAuth,
					StreamType:        resolveStreamType(m),
					SSE:               getSSE(m),
					WSMode:            getWSMode(m),
					MCP:               mcpEnabled,
					MCPSet:            mcpSet,
					MCPScope:          getMCPScope(m),
					MCPDescription:    getMCPDescription(m),
					LeadingComment:    comments.method(svcIdx, methodIdx),
				}
				service.Methods = append(service.Methods, method)
			}

			if len(service.Methods) > 0 {
				api.Services = append(api.Services, service)
			}
		}
	}

	if err := validate(api, msgMap); err != nil {
		return nil, err
	}

	return api, nil
}

func collectNestedMessages(msgMap map[string]*descriptorpb.DescriptorProto, prefix string, msg *descriptorpb.DescriptorProto) {
	for _, nested := range msg.NestedType {
		fqn := prefix + "." + nested.GetName()
		msgMap[fqn] = nested
		collectNestedMessages(msgMap, fqn, nested)
	}
}

func collectNestedEnums(enumMap map[string]*descriptorpb.EnumDescriptorProto, prefix string, msg *descriptorpb.DescriptorProto) {
	for _, enum := range msg.EnumType {
		fqn := prefix + "." + enum.GetName()
		enumMap[fqn] = enum
	}
	for _, nested := range msg.NestedType {
		fqn := prefix + "." + nested.GetName()
		collectNestedEnums(enumMap, fqn, nested)
	}
}

func resolveMessageType(msgMap map[string]*descriptorpb.DescriptorProto, enumMap map[string]*descriptorpb.EnumDescriptorProto, typeName string) *MessageType {
	desc, ok := msgMap[typeName]
	if !ok {
		return &MessageType{
			Name:     lastSegment(typeName),
			FullName: typeName,
		}
	}

	mt := &MessageType{
		Name:     desc.GetName(),
		FullName: typeName,
	}

	for _, f := range desc.Field {
		field := &Field{
			Name:       f.GetName(),
			Number:     f.GetNumber(),
			Type:       f.GetType(),
			TypeName:   f.GetTypeName(),
			Required:   getFieldRequired(f),
			OneofIndex: f.OneofIndex,
			Repeated:   f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED,
		}
		// For enum fields, collect values (excluding 0-value member).
		if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
			if enumDesc, ok := enumMap[f.GetTypeName()]; ok {
				for _, v := range enumDesc.Value {
					if v.GetNumber() == 0 {
						continue // skip unset/default 0-value
					}
					ev := &EnumValue{
						Name:     v.GetName(),
						Number:   v.GetNumber(),
						XVarName: getXVarName(v),
					}
					field.EnumValues = append(field.EnumValues, ev)
				}
			}
		}
		mt.Fields = append(mt.Fields, field)
	}

	for i, od := range desc.OneofDecl {
		decl := &OneofDecl{Name: od.GetName()}
		for _, f := range desc.Field {
			if f.OneofIndex != nil && int(*f.OneofIndex) == i {
				variant := &OneofVariant{
					FieldName: f.GetName(),
					IsMessage: f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
				}
				if variant.IsMessage {
					variant.MessageName = lastSegment(f.GetTypeName())
				}
				decl.Variants = append(decl.Variants, variant)
			}
		}
		mt.OneofDecls = append(mt.OneofDecls, decl)
	}

	return mt
}

func extractHTTPRule(m *descriptorpb.MethodDescriptorProto) (string, string) {
	if m.Options == nil {
		return "", ""
	}

	ext := proto.GetExtension(m.Options, annotations.E_Http)
	rule, ok := ext.(*annotations.HttpRule)
	if !ok || rule == nil {
		return "", ""
	}

	switch p := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		return "GET", p.Get
	case *annotations.HttpRule_Post:
		return "POST", p.Post
	case *annotations.HttpRule_Put:
		return "PUT", p.Put
	case *annotations.HttpRule_Delete:
		return "DELETE", p.Delete
	case *annotations.HttpRule_Patch:
		return "PATCH", p.Patch
	}
	return "", ""
}

func extractPathParams(path string) []string {
	var params []string
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			params = append(params, segment[1:len(segment)-1])
		}
	}
	return params
}

func resolveStreamType(m *descriptorpb.MethodDescriptorProto) StreamType {
	cs := m.GetClientStreaming()
	ss := m.GetServerStreaming()
	switch {
	case cs && ss:
		return StreamBidi
	case cs:
		return StreamClient
	case ss:
		return StreamServer
	default:
		return StreamUnary
	}
}

// extractGoPackage returns the Go import path from the file's go_package option.
// Handles both "github.com/foo/bar" and "github.com/foo/bar;alias" formats.
func extractGoPackage(file *descriptorpb.FileDescriptorProto) string {
	goPkg := file.GetOptions().GetGoPackage()
	if goPkg == "" {
		return ""
	}
	// go_package may contain ";alias" suffix – strip it.
	if idx := strings.Index(goPkg, ";"); idx != -1 {
		goPkg = goPkg[:idx]
	}
	return goPkg
}

func lastSegment(fqn string) string {
	parts := strings.Split(fqn, ".")
	return parts[len(parts)-1]
}
