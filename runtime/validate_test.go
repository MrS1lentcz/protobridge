package runtime_test

import (
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestValidateRequired_AllPresent(t *testing.T) {
	msg := &pb.SimpleRequest{
		Name:   "alice",
		Age:    30,
		Status: pb.Status_STATUS_ACTIVE,
	}
	violations := runtime.ValidateRequired(msg, []string{"name", "age", "status"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestValidateRequired_MissingString(t *testing.T) {
	msg := &pb.SimpleRequest{Age: 30}
	violations := runtime.ValidateRequired(msg, []string{"name"})
	if len(violations) != 1 || violations[0].Field != "name" {
		t.Fatalf("expected violation for 'name', got %v", violations)
	}
}

func TestValidateRequired_MissingInt(t *testing.T) {
	msg := &pb.SimpleRequest{Name: "alice"}
	violations := runtime.ValidateRequired(msg, []string{"age"})
	if len(violations) != 1 || violations[0].Field != "age" {
		t.Fatalf("expected violation for 'age', got %v", violations)
	}
}

func TestValidateRequired_EnumZeroIsInvalid(t *testing.T) {
	msg := &pb.SimpleRequest{
		Name:   "alice",
		Age:    30,
		Status: pb.Status_STATUS_UNSPECIFIED, // 0 value
	}
	violations := runtime.ValidateRequired(msg, []string{"status"})
	if len(violations) != 1 || violations[0].Field != "status" {
		t.Fatalf("expected violation for 'status' (enum 0), got %v", violations)
	}
}

func TestValidateRequired_EnumNonZeroIsValid(t *testing.T) {
	msg := &pb.SimpleRequest{
		Name:   "alice",
		Age:    30,
		Status: pb.Status_STATUS_INACTIVE,
	}
	violations := runtime.ValidateRequired(msg, []string{"status"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-zero enum, got %v", violations)
	}
}

func TestValidateRequired_MultipleViolations(t *testing.T) {
	msg := &pb.SimpleRequest{}
	violations := runtime.ValidateRequired(msg, []string{"name", "age", "status"})
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %v", len(violations), violations)
	}
}

func TestValidateRequired_BoolField_False(t *testing.T) {
	msg := &pb.AllTypesRequest{BoolVal: false}
	violations := runtime.ValidateRequired(msg, []string{"bool_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for false bool, got %d", len(violations))
	}
}

func TestValidateRequired_BoolField_True(t *testing.T) {
	msg := &pb.AllTypesRequest{BoolVal: true}
	violations := runtime.ValidateRequired(msg, []string{"bool_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for true bool, got %d", len(violations))
	}
}

func TestValidateRequired_BytesField_Empty(t *testing.T) {
	msg := &pb.AllTypesRequest{BytesVal: nil}
	violations := runtime.ValidateRequired(msg, []string{"bytes_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for nil bytes, got %d", len(violations))
	}
}

func TestValidateRequired_BytesField_Present(t *testing.T) {
	msg := &pb.AllTypesRequest{BytesVal: []byte("data")}
	violations := runtime.ValidateRequired(msg, []string{"bytes_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-empty bytes, got %d", len(violations))
	}
}

func TestValidateRequired_FloatField_Zero(t *testing.T) {
	msg := &pb.AllTypesRequest{FloatVal: 0.0}
	violations := runtime.ValidateRequired(msg, []string{"float_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for zero float, got %d", len(violations))
	}
}

func TestValidateRequired_FloatField_NonZero(t *testing.T) {
	msg := &pb.AllTypesRequest{FloatVal: 1.5}
	violations := runtime.ValidateRequired(msg, []string{"float_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-zero float, got %d", len(violations))
	}
}

func TestValidateRequired_DoubleField_Zero(t *testing.T) {
	msg := &pb.AllTypesRequest{DoubleVal: 0.0}
	violations := runtime.ValidateRequired(msg, []string{"double_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for zero double, got %d", len(violations))
	}
}

func TestValidateRequired_DoubleField_NonZero(t *testing.T) {
	msg := &pb.AllTypesRequest{DoubleVal: 2.5}
	violations := runtime.ValidateRequired(msg, []string{"double_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-zero double, got %d", len(violations))
	}
}

func TestValidateRequired_Uint32Field_Zero(t *testing.T) {
	msg := &pb.AllTypesRequest{Uint32Val: 0}
	violations := runtime.ValidateRequired(msg, []string{"uint32_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for zero uint32, got %d", len(violations))
	}
}

func TestValidateRequired_Uint32Field_NonZero(t *testing.T) {
	msg := &pb.AllTypesRequest{Uint32Val: 42}
	violations := runtime.ValidateRequired(msg, []string{"uint32_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-zero uint32, got %d", len(violations))
	}
}

func TestValidateRequired_Uint64Field_Zero(t *testing.T) {
	msg := &pb.AllTypesRequest{Uint64Val: 0}
	violations := runtime.ValidateRequired(msg, []string{"uint64_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for zero uint64, got %d", len(violations))
	}
}

func TestValidateRequired_Int64Field_NonZero(t *testing.T) {
	msg := &pb.AllTypesRequest{Int64Val: 100}
	violations := runtime.ValidateRequired(msg, []string{"int64_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for non-zero int64, got %d", len(violations))
	}
}

func TestValidateRequired_MessageField_Nil(t *testing.T) {
	msg := &pb.AllTypesRequest{MsgVal: nil}
	violations := runtime.ValidateRequired(msg, []string{"msg_val"})
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for nil message, got %d", len(violations))
	}
}

func TestValidateRequired_MessageField_Present(t *testing.T) {
	msg := &pb.AllTypesRequest{MsgVal: &pb.Paging{Page: 1, Limit: 10}}
	violations := runtime.ValidateRequired(msg, []string{"msg_val"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for present message, got %d", len(violations))
	}
}

func TestValidateRequired_UnknownField(t *testing.T) {
	msg := &pb.SimpleRequest{Name: "test"}
	violations := runtime.ValidateRequired(msg, []string{"nonexistent_field"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations for unknown field, got %d", len(violations))
	}
}
