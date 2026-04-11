package runtime

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ValidateRequired checks that the given fields are not zero-valued in the
// message. Returns a list of violations for fields that are missing.
func ValidateRequired(msg proto.Message, fields []string) []FieldError {
	ref := msg.ProtoReflect()
	desc := ref.Descriptor()

	var violations []FieldError
	for _, name := range fields {
		fd := desc.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			continue
		}
		val := ref.Get(fd)
		if !ref.Has(fd) || isZero(fd, val) {
			violations = append(violations, FieldError{
				Field:  name,
				Reason: "required",
			})
		}
	}
	return violations
}

func isZero(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return val.String() == ""
	case protoreflect.BytesKind:
		return len(val.Bytes()) == 0
	case protoreflect.BoolKind:
		return !val.Bool()
	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return val.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		return val.Uint() == 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return val.Float() == 0
	case protoreflect.EnumKind:
		return val.Enum() == 0
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return !val.Message().IsValid()
	}
	return false
}
