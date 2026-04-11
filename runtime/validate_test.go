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
