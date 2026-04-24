package generator

import (
	"fmt"
	"strings"
	"unicode"

	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// GenerateOpenAPI produces an OpenAPI 3.1 YAML spec from the ParsedAPI.
//
// Schema emission walks the reference graph: every RPC response type is a
// seed; request types are seeds only for methods that carry a body
// (POST/PUT/PATCH), because GET/DELETE inputs never appear in any $ref.
// Nested messages reachable through MESSAGE-typed fields are emitted
// transitively so the resulting document is self-contained. Well-known
// types (google.protobuf.*) are rendered inline using the conventional
// OpenAPI mapping shared by protoc-gen-openapiv2 and buf; proto map<K,V>
// fields render as object+additionalProperties; proto oneof renders with
// a JSON Schema oneOf constraint so consumers see the discriminated union
// rather than a lossy comment.
//
// Component keys are derived from each message's FullName: short name when
// unique across the emitted set, PascalCase-qualified when two messages
// share a short name across proto packages (otherwise the YAML would have
// duplicate keys and $refs would resolve ambiguously).
func GenerateOpenAPI(api *parser.ParsedAPI) string {
	index := buildMessageIndex(api)
	emitted := collectOpenAPISchemas(api, index)
	ids := buildSchemaIDs(emitted)

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
			if m.StreamType != parser.StreamUnary || m.HTTPMethod == "" {
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
			writePathMethod(&b, pm.svc, pm.method, ids)
		}
	}

	b.WriteString("components:\n")
	b.WriteString("  schemas:\n")
	for _, mt := range emitted {
		writeSchema(&b, mt, index, ids)
	}

	return b.String()
}

// buildMessageIndex returns a FQN → MessageType map. Prefers the parser-
// built api.Messages; falls back to harvesting RPC Input/Output pointers
// when tests construct ParsedAPI without the index.
func buildMessageIndex(api *parser.ParsedAPI) map[string]*parser.MessageType {
	if len(api.Messages) > 0 {
		return api.Messages
	}
	index := make(map[string]*parser.MessageType)
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.InputType != nil && m.InputType.FullName != "" {
				index[m.InputType.FullName] = m.InputType
			}
			if m.OutputType != nil && m.OutputType.FullName != "" {
				index[m.OutputType.FullName] = m.OutputType
			}
		}
	}
	return index
}

// collectOpenAPISchemas returns every MessageType that must appear in
// components.schemas, in BFS discovery order. Seeds: OutputType of every
// unary HTTP method, plus InputType when the method carries a body
// (POST/PUT/PATCH) — GET/DELETE inputs are never $ref'd. Expansion follows
// MESSAGE-typed fields through the index, skipping WKTs and map entries.
func collectOpenAPISchemas(api *parser.ParsedAPI, index map[string]*parser.MessageType) []*parser.MessageType {
	seen := make(map[string]bool)
	var out []*parser.MessageType
	var queue []*parser.MessageType

	enqueue := func(mt *parser.MessageType) {
		if mt == nil || mt.FullName == "" {
			return
		}
		if isWellKnown(mt.FullName) || mt.MapEntry {
			return
		}
		if seen[mt.FullName] {
			return
		}
		seen[mt.FullName] = true
		queue = append(queue, mt)
	}

	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.StreamType != parser.StreamUnary || m.HTTPMethod == "" {
				continue
			}
			if hasRequestBody(m.HTTPMethod) {
				enqueue(m.InputType)
			}
			enqueue(m.OutputType)
		}
	}

	for len(queue) > 0 {
		mt := queue[0]
		queue = queue[1:]
		out = append(out, mt)
		for _, f := range mt.Fields {
			walkFieldTargets(f, index, enqueue)
		}
	}
	return out
}

func hasRequestBody(httpMethod string) bool {
	return httpMethod == "POST" || httpMethod == "PUT" || httpMethod == "PATCH"
}

// walkFieldTargets enqueues every MessageType reachable from f. Map entry
// messages are unwrapped to their value-field target — the synthetic Entry
// itself is never emitted.
func walkFieldTargets(f *parser.Field, index map[string]*parser.MessageType, enqueue func(*parser.MessageType)) {
	if f.Type != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		return
	}
	if isWellKnown(f.TypeName) {
		return
	}
	target, ok := index[f.TypeName]
	if !ok {
		return
	}
	if target.MapEntry {
		for _, vf := range target.Fields {
			if vf.Number == 2 {
				walkFieldTargets(vf, index, enqueue)
				return
			}
		}
		return
	}
	enqueue(target)
}

// buildSchemaIDs assigns a stable component-key ID to each emitted
// MessageType. Short name wins when unique across the emitted set;
// collisions (same short name in different proto packages) fall back to a
// PascalCase concatenation of the FQN so every ID stays globally unique.
// Keyed by FullName so callers can look up the ID from any MessageType
// pointer or TypeName reference.
func buildSchemaIDs(emitted []*parser.MessageType) map[string]string {
	byShort := make(map[string]int, len(emitted))
	for _, mt := range emitted {
		byShort[mt.Name]++
	}
	ids := make(map[string]string, len(emitted))
	for _, mt := range emitted {
		if byShort[mt.Name] > 1 {
			ids[mt.FullName] = qualifiedID(mt.FullName)
		} else {
			ids[mt.FullName] = mt.Name
		}
	}
	return ids
}

// qualifiedID turns a proto FQN into a unique PascalCase identifier safe
// for OpenAPI component keys and downstream codegen (no dots, no dashes).
// ".taskboard.v1.Task" → "TaskboardV1Task".
func qualifiedID(fqn string) string {
	parts := strings.Split(strings.TrimPrefix(fqn, "."), ".")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// schemaRef resolves a proto TypeName to the OpenAPI component key that
// was assigned during buildSchemaIDs. Falls back to the unqualified tail
// of TypeName when the target isn't in the index — that preserves the
// previous shallow behavior for tests that build synthetic ParsedAPIs
// without populating Messages, while real runs always hit the indexed path.
func schemaRef(typeName string, index map[string]*parser.MessageType, ids map[string]string) string {
	if target, ok := index[typeName]; ok {
		if id, ok := ids[target.FullName]; ok {
			return id
		}
		return target.Name
	}
	return lastSegment(typeName)
}

// schemaRefForType is the MessageType-pointer variant of schemaRef, for
// callers holding a concrete InputType/OutputType rather than a TypeName
// string.
func schemaRefForType(mt *parser.MessageType, ids map[string]string) string {
	if mt == nil {
		return ""
	}
	if id, ok := ids[mt.FullName]; ok {
		return id
	}
	return mt.Name
}

// writeMessageSchemaRef emits the schema for a top-level RPC input/output
// message at the given indent. Well-known types render inline (same
// mapping as field-position WKTs) because the BFS collector deliberately
// skips them — emitting a $ref: '#/components/schemas/Empty' here would
// leave the reference unresolvable downstream. Non-WKT messages get a
// $ref pointing at the component key assigned by buildSchemaIDs.
func writeMessageSchemaRef(b *strings.Builder, mt *parser.MessageType, indent string, ids map[string]string) {
	if mt == nil {
		return
	}
	if writeWellKnownInline(b, mt.FullName, indent) {
		return
	}
	fmt.Fprintf(b, "%s$ref: '#/components/schemas/%s'\n", indent, schemaRefForType(mt, ids))
}

func writePathMethod(b *strings.Builder, svc *parser.Service, m *parser.Method, ids map[string]string) {
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
	if hasRequestBody(m.HTTPMethod) && m.InputType != nil {
		fmt.Fprintf(b, "      requestBody:\n")
		fmt.Fprintf(b, "        required: true\n")
		fmt.Fprintf(b, "        content:\n")
		fmt.Fprintf(b, "          application/json:\n")
		fmt.Fprintf(b, "            schema:\n")
		writeMessageSchemaRef(b, m.InputType, "              ", ids)
	}

	// Response
	fmt.Fprintf(b, "      responses:\n")
	fmt.Fprintf(b, "        '200':\n")
	fmt.Fprintf(b, "          description: Successful response\n")
	fmt.Fprintf(b, "          content:\n")
	fmt.Fprintf(b, "            application/json:\n")
	fmt.Fprintf(b, "              schema:\n")
	if m.OutputType != nil {
		writeMessageSchemaRef(b, m.OutputType, "                ", ids)
	}
	fmt.Fprintf(b, "        '400':\n")
	fmt.Fprintf(b, "          description: Bad Request\n")
	fmt.Fprintf(b, "        '401':\n")
	fmt.Fprintf(b, "          description: Unauthorized\n")
	fmt.Fprintf(b, "        '422':\n")
	fmt.Fprintf(b, "          description: Validation Error\n")
}

func writeSchema(b *strings.Builder, mt *parser.MessageType, index map[string]*parser.MessageType, ids map[string]string) {
	fmt.Fprintf(b, "    %s:\n", schemaRefForType(mt, ids))
	fmt.Fprintf(b, "      type: object\n")

	// required[] only carries non-oneof fields — oneof "exactly one"
	// semantics are expressed via the oneOf constraint below, not by
	// listing the variants here (listing them would imply they are all
	// required simultaneously, which contradicts oneof).
	var required []string
	for _, f := range mt.Fields {
		if f.Required && f.OneofIndex == nil {
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
			fmt.Fprintf(b, "        %s:\n", f.Name)
			writeFieldType(b, f, "          ", index, ids)
		}
	}

	var decls []*parser.OneofDecl
	for _, od := range mt.OneofDecls {
		if len(od.Variants) > 0 {
			decls = append(decls, od)
		}
	}
	switch len(decls) {
	case 0:
		// no constraint
	case 1:
		writeOneOfBlock(b, decls[0], "      ")
	default:
		// Multiple oneofs can't share a single oneOf keyword — wrap each
		// in its own subschema under allOf so every decl is enforced
		// independently.
		fmt.Fprintf(b, "      allOf:\n")
		for _, od := range decls {
			writeOneOfBlock(b, od, "        - ")
		}
	}
}

func writeOneOfBlock(b *strings.Builder, od *parser.OneofDecl, indent string) {
	fmt.Fprintf(b, "%soneOf:\n", indent)
	// Every subsequent line of the allOf-list case needs the same indent
	// as the "- " marker consumed, so compute the continuation column.
	cont := strings.Repeat(" ", len(indent))
	for _, v := range od.Variants {
		fmt.Fprintf(b, "%s  - required:\n", cont)
		fmt.Fprintf(b, "%s      - %s\n", cont, v.FieldName)
	}
}

func writeFieldType(b *strings.Builder, f *parser.Field, indent string, index map[string]*parser.MessageType, ids map[string]string) {
	// Map fields are wire-level a repeated MESSAGE of a synthetic *Entry;
	// short-circuit to additionalProperties before the Repeated branch
	// turns them into an array of entries.
	if f.Type == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		if entry, ok := index[f.TypeName]; ok && entry.MapEntry {
			writeMapField(b, entry, indent, index, ids)
			return
		}
	}

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
		if writeWellKnownInline(b, f.TypeName, indent) {
			return
		}
		fmt.Fprintf(b, "%s$ref: '#/components/schemas/%s'\n", indent, schemaRef(f.TypeName, index, ids))
	default:
		fmt.Fprintf(b, "%stype: string\n", indent)
	}
}

func writeMapField(b *strings.Builder, entry *parser.MessageType, indent string, index map[string]*parser.MessageType, ids map[string]string) {
	fmt.Fprintf(b, "%stype: object\n", indent)
	fmt.Fprintf(b, "%sadditionalProperties:\n", indent)

	// Map entry messages have exactly two fields by proto convention:
	// key=tag 1, value=tag 2. Render the value shape and bail — the key
	// type is always a scalar the JSON representation stringifies anyway.
	var value *parser.Field
	for _, vf := range entry.Fields {
		if vf.Number == 2 {
			value = vf
			break
		}
	}
	if value == nil {
		fmt.Fprintf(b, "%s  type: string\n", indent)
		return
	}
	// Entry fields don't carry the outer map's synthetic Repeated flag,
	// but be defensive — a copy with Repeated=false keeps writeFieldType
	// simple if the parser ever changes.
	vf := *value
	vf.Repeated = false
	writeFieldType(b, &vf, indent+"  ", index, ids)
}

// isWellKnown reports whether typeName is a google.protobuf.* type that
// gets rendered inline rather than via $ref.
func isWellKnown(typeName string) bool {
	return strings.HasPrefix(typeName, ".google.protobuf.")
}

// writeWellKnownInline renders the OpenAPI schema for a google.protobuf.*
// type and returns true when it recognized the name. The mapping matches
// protoc-gen-openapiv2 / buf so generated specs stay portable for the
// usual downstream tooling (openapi-generator, Spectral, Redocly).
func writeWellKnownInline(b *strings.Builder, typeName, indent string) bool {
	switch typeName {
	case ".google.protobuf.Timestamp":
		fmt.Fprintf(b, "%stype: string\n", indent)
		fmt.Fprintf(b, "%sformat: date-time\n", indent)
	case ".google.protobuf.Duration":
		fmt.Fprintf(b, "%stype: string\n", indent)
	case ".google.protobuf.FieldMask":
		fmt.Fprintf(b, "%stype: string\n", indent)
	case ".google.protobuf.Empty":
		fmt.Fprintf(b, "%stype: object\n", indent)
	case ".google.protobuf.Struct":
		fmt.Fprintf(b, "%stype: object\n", indent)
		fmt.Fprintf(b, "%sadditionalProperties: true\n", indent)
	case ".google.protobuf.Any":
		fmt.Fprintf(b, "%stype: object\n", indent)
		fmt.Fprintf(b, "%sadditionalProperties: true\n", indent)
	case ".google.protobuf.Value":
		// Any JSON value — no constraints.
		fmt.Fprintf(b, "%s{}\n", indent)
	case ".google.protobuf.ListValue":
		fmt.Fprintf(b, "%stype: array\n", indent)
		fmt.Fprintf(b, "%sitems: {}\n", indent)
	case ".google.protobuf.BoolValue":
		fmt.Fprintf(b, "%stype: boolean\n", indent)
	case ".google.protobuf.StringValue":
		fmt.Fprintf(b, "%stype: string\n", indent)
	case ".google.protobuf.BytesValue":
		fmt.Fprintf(b, "%stype: string\n", indent)
		fmt.Fprintf(b, "%sformat: byte\n", indent)
	case ".google.protobuf.Int32Value":
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: int32\n", indent)
	case ".google.protobuf.Int64Value":
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: int64\n", indent)
	case ".google.protobuf.UInt32Value":
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: uint32\n", indent)
	case ".google.protobuf.UInt64Value":
		fmt.Fprintf(b, "%stype: integer\n", indent)
		fmt.Fprintf(b, "%sformat: uint64\n", indent)
	case ".google.protobuf.FloatValue":
		fmt.Fprintf(b, "%stype: number\n", indent)
		fmt.Fprintf(b, "%sformat: float\n", indent)
	case ".google.protobuf.DoubleValue":
		fmt.Fprintf(b, "%stype: number\n", indent)
		fmt.Fprintf(b, "%sformat: double\n", indent)
	default:
		return false
	}
	return true
}

func lastSegment(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return name
	}
	return name[i+1:]
}
