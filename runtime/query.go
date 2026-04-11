package runtime

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DecodeQueryParams maps URL query parameters into a nested message field
// of the request proto. The targetField is the name of the field that holds
// the query params message (e.g. "paging").
func DecodeQueryParams(r *http.Request, msg proto.Message, targetField string) error {
	ref := msg.ProtoReflect()
	fd := ref.Descriptor().Fields().ByName(protoreflect.Name(targetField))
	if fd == nil {
		return fmt.Errorf("query_params_target field %q not found", targetField)
	}
	if fd.Kind() != protoreflect.MessageKind {
		return fmt.Errorf("query_params_target field %q is not a message type", targetField)
	}

	targetMsg := ref.Mutable(fd).Message()
	targetDesc := targetMsg.Descriptor()

	for key, values := range r.URL.Query() {
		if len(values) == 0 {
			continue
		}
		val := values[0]

		// Support dotted keys like "paging.page" → strip the target prefix.
		fieldName := key
		if strings.HasPrefix(key, targetField+".") {
			fieldName = key[len(targetField)+1:]
		}

		fieldDesc := targetDesc.Fields().ByName(protoreflect.Name(fieldName))
		if fieldDesc == nil {
			continue
		}

		pv, err := parseFieldValue(fieldDesc, val)
		if err != nil {
			return fmt.Errorf("query param %q: %w", key, err)
		}
		targetMsg.Set(fieldDesc, pv)
	}

	return nil
}

func parseFieldValue(fd protoreflect.FieldDescriptor, s string) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(s), nil
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid bool: %w", err)
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid int32: %w", err)
		}
		return protoreflect.ValueOfInt32(int32(n)), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid int64: %w", err)
		}
		return protoreflect.ValueOfInt64(n), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid uint32: %w", err)
		}
		return protoreflect.ValueOfUint32(uint32(n)), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid uint64: %w", err)
		}
		return protoreflect.ValueOfUint64(n), nil
	case protoreflect.FloatKind:
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid float: %w", err)
		}
		return protoreflect.ValueOfFloat32(float32(f)), nil
	case protoreflect.DoubleKind:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("invalid double: %w", err)
		}
		return protoreflect.ValueOfFloat64(f), nil
	case protoreflect.EnumKind:
		ev := fd.Enum().Values().ByName(protoreflect.Name(s))
		if ev == nil {
			// Try parsing as number.
			n, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return protoreflect.Value{}, fmt.Errorf("invalid enum value: %s", s)
			}
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)), nil
		}
		return protoreflect.ValueOfEnum(ev.Number()), nil
	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported query param type: %s", fd.Kind())
	}
}
