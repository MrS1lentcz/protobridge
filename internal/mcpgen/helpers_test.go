package mcpgen

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	parserpkg "github.com/mrs1lentcz/protobridge/internal/parser"
)

func strPtr(s string) *string { return &s }

func TestToSnakeCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GetOpenTasks", "get_open_tasks"},
		{"X", "x"},
		{"Already_snake", "already_snake"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := toSnakeCase(tc.in); got != tc.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToScreamingSnake(t *testing.T) {
	cases := []struct{ in, want string }{
		{"TaskService", "TASK_SERVICE"},
		{"TS", "T_S"}, // simple per-uppercase-letter splitter — TS → T_S; refactor if it becomes a problem
		{"x", "X"},
	}
	for _, tc := range cases {
		if got := toScreamingSnake(tc.in); got != tc.want {
			t.Errorf("toScreamingSnake(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToLowerCamel(t *testing.T) {
	if got := toLowerCamel("TaskService"); got != "taskService" {
		t.Errorf("got %q", got)
	}
	if got := toLowerCamel(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestRun_InvalidProto(t *testing.T) {
	resp := Run(bytes.NewReader([]byte("not a proto")))
	if resp.Error == nil {
		t.Error("expected error for invalid CodeGeneratorRequest payload")
	}
}

func TestRun_ReadError(t *testing.T) {
	// Reader that errors on Read — exercises the io.ReadAll error path.
	resp := Run(&errReader{})
	if resp.Error == nil {
		t.Error("expected error from failing reader")
	}
}

func TestRun_BadParameter(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{Parameter: strPtr("bogus=x")}
	data, _ := proto.Marshal(req)
	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Error("expected error for unknown plugin option")
	}
}

func TestProtoTypeRef_NilAndForms(t *testing.T) {
	if got := protoTypeRef(nil); got != "pb.Unknown" {
		t.Errorf("nil → %q", got)
	}
	if got := protoTypeRef(&parserpkg.MessageType{Name: "X", FullName: "google.protobuf.Empty"}); got != "emptypb.Empty" {
		t.Errorf("unprefixed Empty → %q", got)
	}
}

func TestProtoImportPath_FallbackToProtoPackage(t *testing.T) {
	got := protoImportPath(&parserpkg.Service{ProtoPackage: "x.v1.sub"})
	if got != "x/v1/sub" {
		t.Errorf("got %q", got)
	}
}

func TestIsEmpty(t *testing.T) {
	if isEmpty(nil) {
		t.Error("nil should not be Empty")
	}
	if !isEmpty(&parserpkg.MessageType{FullName: "google.protobuf.Empty"}) {
		t.Error("unprefixed FullName should still match")
	}
}

func TestLastSegmentOfTypeName(t *testing.T) {
	if got := lastSegmentOfTypeName(".x.v1.Foo"); got != "Foo" {
		t.Errorf("got %q", got)
	}
	if got := lastSegmentOfTypeName("Plain"); got != "Plain" {
		t.Errorf("got %q", got)
	}
}

type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) { return 0, errReadFailed }

var errReadFailed = errStr("read failed")

type errStr string

func (e errStr) Error() string { return string(e) }
