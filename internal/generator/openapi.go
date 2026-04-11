package generator

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// GenerateOpenAPI produces an OpenAPI 3.1 YAML spec from the ParsedAPI.
func GenerateOpenAPI(api *parser.ParsedAPI) string {
	var b strings.Builder

	b.WriteString("openapi: '3.1.0'\n")
	b.WriteString("info:\n")
	b.WriteString("  title: protobridge API\n")
	b.WriteString("  version: '1.0.0'\n")
	b.WriteString("paths:\n")

	// Group methods by HTTP path to avoid duplicate YAML keys.
	type pathMethod struct {
		svc    *parser.Service
		method *parser.Method
	}
	pathGroups := make(map[string][]pathMethod)
	var pathOrder []string
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.StreamType != parser.StreamUnary {
				continue
			}
			if _, exists := pathGroups[m.HTTPPath]; !exists {
				pathOrder = append(pathOrder, m.HTTPPath)
			}
			pathGroups[m.HTTPPath] = append(pathGroups[m.HTTPPath], pathMethod{svc, m})
		}
	}
	for _, path := range pathOrder {
		fmt.Fprintf(&b, "  %s:\n", path)
		for _, pm := range pathGroups[path] {
			writePathMethod(&b, pm.svc, pm.method)
		}
	}

	b.WriteString("components:\n")
	b.WriteString("  schemas:\n")

	written := make(map[string]bool)
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.InputType != nil && !written[m.InputType.Name] {
				writeSchema(&b, m.InputType)
				written[m.InputType.Name] = true
			}
			if m.OutputType != nil && !written[m.OutputType.Name] {
				writeSchema(&b, m.OutputType)
				written[m.OutputType.Name] = true
			}
		}
	}

	return b.String()
}

func writePathMethod(b *strings.Builder, svc *parser.Service, m *parser.Method) {
	method := strings.ToLower(m.HTTPMethod)

	fmt.Fprintf(b, "    %s:\n", method)
	tagName := svc.Name
	if svc.DisplayName != "" {
		tagName = svc.DisplayName
	}
	fmt.Fprintf(b, "      operationId: %s_%s\n", svc.Name, m.Name)
	fmt.Fprintf(b, "      tags:\n")
	fmt.Fprintf(b, "        - %s\n", tagName)

	if !m.ExcludeAuth {
		fmt.Fprintf(b, "      security:\n")
		fmt.Fprintf(b, "        - bearerAuth: []\n")
	}

	// Parameters: path params + required headers + query params
	hasParams := len(m.PathParams) > 0 || len(m.RequiredHeaders) > 0 || m.QueryParamsTarget != ""
	if hasParams {
		fmt.Fprintf(b, "      parameters:\n")
		for _, p := range m.PathParams {
			fmt.Fprintf(b, "        - name: %s\n", p)
			fmt.Fprintf(b, "          in: path\n")
			fmt.Fprintf(b, "          required: true\n")
			fmt.Fprintf(b, "          schema:\n")
			fmt.Fprintf(b, "            type: string\n")
		}
		for _, h := range m.RequiredHeaders {
			fmt.Fprintf(b, "        - name: %s\n", h)
			fmt.Fprintf(b, "          in: header\n")
			fmt.Fprintf(b, "          required: true\n")
			fmt.Fprintf(b, "          schema:\n")
			fmt.Fprintf(b, "            type: string\n")
		}
		if m.QueryParamsTarget != "" && m.InputType != nil {
			for _, f := range m.InputType.Fields {
				if f.Name == m.QueryParamsTarget {
					// Write fields of the nested message as query params.
					// For now, we note this as a reference.
					fmt.Fprintf(b, "        # query params from %s\n", m.QueryParamsTarget)
					break
				}
			}
		}
	}

	// Request body
	if m.HTTPMethod == "POST" || m.HTTPMethod == "PUT" || m.HTTPMethod == "PATCH" {
		fmt.Fprintf(b, "      requestBody:\n")
		fmt.Fprintf(b, "        required: true\n")
		fmt.Fprintf(b, "        content:\n")
		fmt.Fprintf(b, "          application/json:\n")
		fmt.Fprintf(b, "            schema:\n")
		if m.InputType != nil {
			fmt.Fprintf(b, "              $ref: '#/components/schemas/%s'\n", m.InputType.Name)
		}
	}

	// Response
	fmt.Fprintf(b, "      responses:\n")
	fmt.Fprintf(b, "        '200':\n")
	fmt.Fprintf(b, "          description: Successful response\n")
	fmt.Fprintf(b, "          content:\n")
	fmt.Fprintf(b, "            application/json:\n")
	fmt.Fprintf(b, "              schema:\n")
	if m.OutputType != nil {
		fmt.Fprintf(b, "                $ref: '#/components/schemas/%s'\n", m.OutputType.Name)
	}
	fmt.Fprintf(b, "        '400':\n")
	fmt.Fprintf(b, "          description: Bad Request\n")
	fmt.Fprintf(b, "        '401':\n")
	fmt.Fprintf(b, "          description: Unauthorized\n")
	fmt.Fprintf(b, "        '422':\n")
	fmt.Fprintf(b, "          description: Validation Error\n")
}

func writeSchema(b *strings.Builder, mt *parser.MessageType) {
	fmt.Fprintf(b, "    %s:\n", mt.Name)
	fmt.Fprintf(b, "      type: object\n")

	// Collect required fields
	var required []string
	for _, f := range mt.Fields {
		if f.Required {
			required = append(required, f.Name)
		}
	}
	if len(required) > 0 {
		fmt.Fprintf(b, "      required:\n")
		for _, r := range required {
			fmt.Fprintf(b, "        - %s\n", r)
		}
	}

	if len(mt.Fields) > 0 {
		fmt.Fprintf(b, "      properties:\n")
		for _, f := range mt.Fields {
			if f.OneofIndex != nil {
				continue // handled via oneOf
			}
			fmt.Fprintf(b, "        %s:\n", f.Name)
			writeFieldType(b, f, "          ")
		}
	}

	// oneOf declarations
	for _, od := range mt.OneofDecls {
		if len(od.Variants) == 0 {
			continue
		}
		fmt.Fprintf(b, "      # oneof: %s\n", od.Name)
	}
}

func writeFieldType(b *strings.Builder, f *parser.Field, indent string) {
	if f.Repeated {
		fmt.Fprintf(b, "%stype: array\n", indent)
		fmt.Fprintf(b, "%sitems:\n", indent)
		indent += "  "
	}

	switch f.Type {
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		fmt.Fprintf(b, "%stype: string\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: int32\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: int64\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: uint32\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: uint64\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		fmt.Fprintf(b, "%stype: number\n", indent)
		fmt.Fprintf(b, "%sformat: float\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		fmt.Fprintf(b, "%stype: number\n", indent)
		fmt.Fprintf(b, "%sformat: double\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		fmt.Fprintf(b, "%stype: boolean\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		fmt.Fprintf(b, "%stype: string\n", indent)
		fmt.Fprintf(b, "%sformat: byte\n", indent)
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		fmt.Fprintf(b, "%stype: string\n", indent)
		if len(f.EnumValues) > 0 {
			fmt.Fprintf(b, "%senum:\n", indent)
			for _, ev := range f.EnumValues {
				name := ev.Name
				if ev.XVarName != "" {
					name = ev.XVarName
				}
				fmt.Fprintf(b, "%s  - %s\n", indent, name)
			}
		}
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		parts := strings.Split(f.TypeName, ".")
		ref := parts[len(parts)-1]
		fmt.Fprintf(b, "%s$ref: '#/components/schemas/%s'\n", indent, ref)
	default:
		fmt.Fprintf(b, "%stype: string\n", indent)
	}
}
