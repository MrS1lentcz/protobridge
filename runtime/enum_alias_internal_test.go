package runtime

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

// These tests exercise the defensive type-assertion failure branches in
// rewriteFieldValue / applyOutputFieldValue / applyEnumAliasesToOutput
// directly. In production the JSON tree is always shaped correctly by
// protojson (input prepass) or json.Marshal (output postprocess), so these
// branches never fire; covering them via direct internal calls keeps a
// regression that breaks the safety net visible in tests.

func TestRewriteFieldValue_MapTypeMismatchSafelyReturns(t *testing.T) {
	desc := (&pb.EnumContainerRequest{}).ProtoReflect().Descriptor()
	mapField := desc.Fields().ByName("by_name")
	if mapField == nil {
		t.Fatal("by_name field not found in descriptor")
	}
	got, changed := rewriteFieldValue("not-a-map", mapField)
	if changed {
		t.Errorf("expected changed=false for type mismatch, got %v", changed)
	}
	if got != "not-a-map" {
		t.Errorf("expected value to pass through unchanged, got %v", got)
	}
}

func TestRewriteFieldValue_RepeatedTypeMismatchSafelyReturns(t *testing.T) {
	desc := (&pb.EnumContainerRequest{}).ProtoReflect().Descriptor()
	repField := desc.Fields().ByName("statuses")
	if repField == nil {
		t.Fatal("statuses field not found")
	}
	got, changed := rewriteFieldValue("not-an-array", repField)
	if changed || got != "not-an-array" {
		t.Errorf("expected unchanged passthrough, got (%v, %v)", got, changed)
	}
}

func TestApplyEnumAliasesToOutput_NonObjectReturnsFalse(t *testing.T) {
	desc := (&pb.SimpleResponse{}).ProtoReflect().Descriptor()
	if changed := applyEnumAliasesToOutput("not-a-map", desc); changed {
		t.Error("expected false for non-object node")
	}
}

func TestApplyOutputFieldValue_TypeMismatchesPassThrough(t *testing.T) {
	desc := (&pb.EnumContainerRequest{}).ProtoReflect().Descriptor()
	for _, tc := range []struct {
		name      string
		fieldName protoreflect.Name
		val       any
	}{
		{"map-non-object", "by_name", "x"},
		{"list-non-array", "statuses", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fd := desc.Fields().ByName(tc.fieldName)
			got, changed := applyOutputFieldValue(tc.val, fd)
			if changed || got != "x" {
				t.Errorf("expected unchanged passthrough, got (%v, %v)", got, changed)
			}
		})
	}
}
