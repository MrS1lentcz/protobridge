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
		// Cached value may be a typed nil map for alias-free enums; that's
		// returned as-is and callers treat nil map equivalently to "no aliases".
		// (The plain `v == nil` check would never fire here because sync.Map
		// wraps the typed nil in a non-nil interface.)
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
// Returns true if any value was rewritten.
//
// JSON keys may be either the proto field name (snake_case) or the JSON name
// (camelCase) — both are accepted by protojson on input, so we look up both.
func rewriteEnumAliases(node any, md protoreflect.MessageDescriptor) bool {
	obj, ok := node.(map[string]any)
	if !ok {
		return false
	}
	fields := md.Fields()
	changed := false
	for key, val := range obj {
		fd := fields.ByJSONName(key)
		if fd == nil {
			fd = fields.ByName(protoreflect.Name(key))
		}
		if fd == nil {
			continue
		}
		newVal, fieldChanged := rewriteFieldValue(val, fd)
		if fieldChanged {
			obj[key] = newVal
			changed = true
		}
	}
	return changed
}

func rewriteFieldValue(val any, fd protoreflect.FieldDescriptor) (any, bool) {
	switch {
	case fd.IsMap():
		valFD := fd.MapValue()
		m, ok := val.(map[string]any)
		if !ok {
			return val, false
		}
		changed := false
		for k, v := range m {
			nv, c := rewriteScalarOrMessage(v, valFD)
			if c {
				m[k] = nv
				changed = true
			}
		}
		return m, changed
	case fd.IsList():
		arr, ok := val.([]any)
		if !ok {
			return val, false
		}
		changed := false
		for i, v := range arr {
			nv, c := rewriteScalarOrMessage(v, fd)
			if c {
				arr[i] = nv
				changed = true
			}
		}
		return arr, changed
	default:
		return rewriteScalarOrMessage(val, fd)
	}
}

func rewriteScalarOrMessage(val any, fd protoreflect.FieldDescriptor) (any, bool) {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		s, ok := val.(string)
		if !ok {
			return val, false
		}
		if aliases := enumAliases(fd.Enum()); aliases != nil {
			if canonical, found := aliases[s]; found {
				return canonical, true
			}
		}
		return val, false
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return val, rewriteEnumAliases(val, fd.Message())
	default:
		return val, false
	}
}

// reverseEnumAliasCache: enum full name → canonical proto name → x_var_name alias.
var reverseEnumAliasCache sync.Map

// messageHasAliasesCache memoises whether a message (transitively, through
// nested messages and map values) contains any enum field whose enum has at
// least one (protobridge.x_var_name) value. When false, we can skip the JSON
// unmarshal + tree walk entirely on every request/response of that type.
var messageHasAliasesCache sync.Map // map[protoreflect.FullName]bool

func messageHasAliases(md protoreflect.MessageDescriptor) bool {
	if v, ok := messageHasAliasesCache.Load(md.FullName()); ok {
		return v.(bool)
	}
	// Pre-store false to break recursion on self-referential message types.
	messageHasAliasesCache.Store(md.FullName(), false)
	result := descriptorHasAliases(md, map[protoreflect.FullName]bool{md.FullName(): true})
	messageHasAliasesCache.Store(md.FullName(), result)
	return result
}

func descriptorHasAliases(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.IsMap() {
			vfd := fd.MapValue()
			if fieldHasAliases(vfd, seen) {
				return true
			}
			continue
		}
		if fieldHasAliases(fd, seen) {
			return true
		}
	}
	return false
}

func fieldHasAliases(fd protoreflect.FieldDescriptor, seen map[protoreflect.FullName]bool) bool {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		return enumAliases(fd.Enum()) != nil || reverseEnumAliases(fd.Enum()) != nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		nested := fd.Message()
		if seen[nested.FullName()] {
			return false
		}
		seen[nested.FullName()] = true
		return descriptorHasAliases(nested, seen)
	}
	return false
}

func reverseEnumAliases(ed protoreflect.EnumDescriptor) map[string]string {
	if v, ok := reverseEnumAliasCache.Load(ed.FullName()); ok {
		// Same typed-nil-in-interface caveat as enumAliases above.
		return v.(map[string]string)
	}
	var rev map[string]string
	values := ed.Values()
	for i := 0; i < values.Len(); i++ {
		v := values.Get(i)
		alias, _ := proto.GetExtension(v.Options(), pboptions.E_XVarName).(string)
		if alias == "" {
			continue
		}
		if rev == nil {
			rev = make(map[string]string)
		}
		rev[string(v.Name())] = alias
	}
	if rev == nil {
		reverseEnumAliasCache.Store(ed.FullName(), (map[string]string)(nil))
		return nil
	}
	reverseEnumAliasCache.Store(ed.FullName(), rev)
	return rev
}

// applyEnumAliasesToOutput is the symmetric counterpart of rewriteEnumAliases:
// it walks a marshaled JSON tree and replaces canonical proto enum names with
// their x_var_name aliases. Returns true if any value was rewritten.
func applyEnumAliasesToOutput(node any, md protoreflect.MessageDescriptor) bool {
	obj, ok := node.(map[string]any)
	if !ok {
		return false
	}
	fields := md.Fields()
	changed := false
	for key, val := range obj {
		fd := fields.ByJSONName(key)
		if fd == nil {
			fd = fields.ByName(protoreflect.Name(key))
		}
		if fd == nil {
			continue
		}
		newVal, fieldChanged := applyOutputFieldValue(val, fd)
		if fieldChanged {
			obj[key] = newVal
			changed = true
		}
	}
	return changed
}

func applyOutputFieldValue(val any, fd protoreflect.FieldDescriptor) (any, bool) {
	switch {
	case fd.IsMap():
		valFD := fd.MapValue()
		m, ok := val.(map[string]any)
		if !ok {
			return val, false
		}
		changed := false
		for k, v := range m {
			nv, c := applyOutputScalarOrMessage(v, valFD)
			if c {
				m[k] = nv
				changed = true
			}
		}
		return m, changed
	case fd.IsList():
		arr, ok := val.([]any)
		if !ok {
			return val, false
		}
		changed := false
		for i, v := range arr {
			nv, c := applyOutputScalarOrMessage(v, fd)
			if c {
				arr[i] = nv
				changed = true
			}
		}
		return arr, changed
	default:
		return applyOutputScalarOrMessage(val, fd)
	}
}

func applyOutputScalarOrMessage(val any, fd protoreflect.FieldDescriptor) (any, bool) {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		s, ok := val.(string)
		if !ok {
			return val, false
		}
		if rev := reverseEnumAliases(fd.Enum()); rev != nil {
			if alias, found := rev[s]; found {
				return alias, true
			}
		}
		return val, false
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return val, applyEnumAliasesToOutput(val, fd.Message())
	default:
		return val, false
	}
}

// postprocessEnumAliases rewrites canonical enum names in marshaled JSON to
// their x_var_name aliases. Returns the original bytes unchanged when there
// is nothing to rewrite.
func postprocessEnumAliases(body []byte, msg proto.Message) []byte {
	md := msg.ProtoReflect().Descriptor()
	if !messageHasAliases(md) {
		return body
	}
	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		return body
	}
	if _, ok := tree.(map[string]any); !ok {
		return body
	}
	if !applyEnumAliasesToOutput(tree, md) {
		return body
	}
	out, err := json.Marshal(tree)
	if err != nil {
		return body
	}
	return out
}

// preprocessEnumAliases parses raw JSON, rewrites any x_var_name aliases on
// enum-typed fields to canonical proto enum names, and returns the rewritten
// JSON. If the body is not a JSON object or no aliases were rewritten, the
// original bytes are returned unchanged (no re-marshal).
func preprocessEnumAliases(body []byte, msg proto.Message) ([]byte, error) {
	md := msg.ProtoReflect().Descriptor()
	if !messageHasAliases(md) {
		return body, nil
	}
	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		return body, nil // let protojson surface the parse error
	}
	if _, ok := tree.(map[string]any); !ok {
		return body, nil
	}
	if !rewriteEnumAliases(tree, md) {
		return body, nil
	}
	return json.Marshal(tree)
}
