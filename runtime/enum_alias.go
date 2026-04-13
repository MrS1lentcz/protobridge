package runtime

import (
	"encoding/json"
	"sync"

	pboptions "github.com/mrs1lentcz/protobridge/proto/protobridge"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// enumAliasCache memoises (custom name → canonical proto enum name) maps per
// EnumDescriptor full name. The descriptor is package-global, so the map is
// safe to share.
var enumAliasCache sync.Map // map[protoreflect.FullName]map[string]string

// enumAliases returns a mapping of x_var_name → canonical enum value name
// for the given enum. Returns nil if no value carries x_var_name.
func enumAliases(ed protoreflect.EnumDescriptor) map[string]string {
	if v, ok := enumAliasCache.Load(ed.FullName()); ok {
		if v == nil {
			return nil
		}
		return v.(map[string]string)
	}
	var aliases map[string]string
	values := ed.Values()
	for i := 0; i < values.Len(); i++ {
		v := values.Get(i)
		alias, _ := proto.GetExtension(v.Options(), pboptions.E_XVarName).(string)
		if alias == "" {
			continue
		}
		if aliases == nil {
			aliases = make(map[string]string)
		}
		aliases[alias] = string(v.Name())
	}
	if aliases == nil {
		enumAliasCache.Store(ed.FullName(), (map[string]string)(nil))
		return nil
	}
	enumAliasCache.Store(ed.FullName(), aliases)
	return aliases
}

// rewriteEnumAliases walks a decoded JSON tree alongside a message descriptor
// and rewrites string values of enum-typed fields from their x_var_name alias
// to the canonical proto enum name, so protojson.Unmarshal accepts them.
//
// JSON keys may be either the proto field name (snake_case) or the JSON name
// (camelCase) — both are accepted by protojson on input, so we look up both.
func rewriteEnumAliases(node any, md protoreflect.MessageDescriptor) any {
	obj, ok := node.(map[string]any)
	if !ok {
		return node
	}
	fields := md.Fields()
	for key, val := range obj {
		fd := fields.ByJSONName(key)
		if fd == nil {
			fd = fields.ByName(protoreflect.Name(key))
		}
		if fd == nil {
			continue
		}
		obj[key] = rewriteFieldValue(val, fd)
	}
	return obj
}

func rewriteFieldValue(val any, fd protoreflect.FieldDescriptor) any {
	switch {
	case fd.IsMap():
		valFD := fd.MapValue()
		m, ok := val.(map[string]any)
		if !ok {
			return val
		}
		for k, v := range m {
			m[k] = rewriteScalarOrMessage(v, valFD)
		}
		return m
	case fd.IsList():
		arr, ok := val.([]any)
		if !ok {
			return val
		}
		for i, v := range arr {
			arr[i] = rewriteScalarOrMessage(v, fd)
		}
		return arr
	default:
		return rewriteScalarOrMessage(val, fd)
	}
}

func rewriteScalarOrMessage(val any, fd protoreflect.FieldDescriptor) any {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		s, ok := val.(string)
		if !ok {
			return val
		}
		if aliases := enumAliases(fd.Enum()); aliases != nil {
			if canonical, found := aliases[s]; found {
				return canonical
			}
		}
		return val
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return rewriteEnumAliases(val, fd.Message())
	default:
		return val
	}
}

// preprocessEnumAliases parses raw JSON, rewrites any x_var_name aliases on
// enum-typed fields to canonical proto enum names, and returns the rewritten
// JSON. If the body is not a JSON object or contains no aliasable enums, the
// original bytes are returned unchanged.
func preprocessEnumAliases(body []byte, msg proto.Message) ([]byte, error) {
	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		return body, nil // let protojson surface the parse error
	}
	if _, ok := tree.(map[string]any); !ok {
		return body, nil
	}
	rewriteEnumAliases(tree, msg.ProtoReflect().Descriptor())
	return json.Marshal(tree)
}
