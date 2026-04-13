package eventsgen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	parserpkg "github.com/mrs1lentcz/protobridge/internal/parser"
	optionspb "github.com/mrs1lentcz/protobridge/proto/protobridge"
)

// TestRun_FullPipeline exercises Run end-to-end against a fully-formed
// CodeGeneratorRequest including the (protobridge.event) extension. Covers
// the proto unmarshal → ParseOptions → Generate path, including the Run
// wiring and errResponse helper.
func TestRun_FullPipeline(t *testing.T) {
	mo := &descriptorpb.MessageOptions{}
	proto.SetExtension(mo, optionspb.E_Event, &optionspb.EventOptions{
		Kind: optionspb.EventKind_BROADCAST,
	})
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"events.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    sp("events.proto"),
			Package: sp("test.v1"),
			Options: &descriptorpb.FileOptions{GoPackage: sp("example.com/test/events")},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name:    sp("Ping"),
				Field:   []*descriptorpb.FieldDescriptorProto{{Name: sp("id"), Number: i32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}},
				Options: mo,
			}},
		}},
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := Run(bytes.NewReader(data))
	if resp.Error != nil {
		t.Fatalf("Run returned error: %s", resp.GetError())
	}
	if len(resp.File) < 2 {
		t.Fatalf("expected events file + asyncapi schema, got %d", len(resp.File))
	}
}

func TestRun_InvalidProtoBytesYieldsErrorResponse(t *testing.T) {
	resp := Run(bytes.NewReader([]byte("not a proto")))
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
}

func TestRun_BadParameter(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{Parameter: sp("bogus=x")}
	data, _ := proto.Marshal(req)
	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Fatal("expected error response for unknown plugin option")
	}
}

func TestParseOptions_SkipsEmptyAndMissingEquals(t *testing.T) {
	if _, err := ParseOptions(",, output_pkg=ev ,,"); err != nil {
		t.Errorf("trailing/empty parts should be tolerated: %v", err)
	}
	if _, err := ParseOptions("output_pkg"); err == nil {
		t.Error("expected error for missing = sign")
	}
}

func TestFilename_RespectsOutputPkgOverride(t *testing.T) {
	cases := []struct {
		pkgPath, outputPkg, want string
	}{
		{"example.com/foo/events", "events", "events_events.go"},
		{"example.com/foo/myapp", "events", "myapp_events.go"},
		// Custom output_pkg adds a directory level to keep the override
		// visible in the layout.
		{"example.com/foo/myapp", "eventspkg", "eventspkg/myapp_events.go"},
	}
	for _, tc := range cases {
		if got := filename(tc.pkgPath, tc.outputPkg); got != tc.want {
			t.Errorf("filename(%q, %q) = %q, want %q", tc.pkgPath, tc.outputPkg, got, tc.want)
		}
	}
}

func TestGenerateEventsFile_EmptyInputErrors(t *testing.T) {
	if _, err := generateEventsFile("example.com/x", nil); err == nil {
		t.Fatal("expected error for empty events slice")
	}
}

// TestAsyncAPI_AllScalarTypes makes sure fieldKindSchema's switch arms
// for every supported proto scalar type are exercised at least once.
func TestAsyncAPI_AllScalarTypes(t *testing.T) {
	mt := &parserpkg.MessageType{
		Name: "AllScalars", FullName: ".x.AllScalars",
		Fields: []*parserpkg.Field{
			{Name: "s", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING},
			{Name: "i32", Type: descriptorpb.FieldDescriptorProto_TYPE_INT32},
			{Name: "i64", Type: descriptorpb.FieldDescriptorProto_TYPE_INT64},
			{Name: "u32", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32},
			{Name: "u64", Type: descriptorpb.FieldDescriptorProto_TYPE_UINT64},
			{Name: "f32", Type: descriptorpb.FieldDescriptorProto_TYPE_FLOAT},
			{Name: "f64", Type: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE},
			{Name: "b", Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL},
			{Name: "raw", Type: descriptorpb.FieldDescriptorProto_TYPE_BYTES},
			{Name: "items", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Repeated: true},
			{
				Name: "color", Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM,
				EnumValues: []*parserpkg.EnumValue{
					{Name: "RED", XVarName: "red"},
					{Name: "BLUE"},
				},
			},
			{Name: "fallback", Type: descriptorpb.FieldDescriptorProto_TYPE_GROUP},
			{Name: "nested", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".x.Inner"},
			{Name: "external", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".other.Thing"},
		},
	}
	inner := &parserpkg.MessageType{
		Name: "Inner", FullName: ".x.Inner",
		Fields: []*parserpkg.Field{{Name: "v", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING}},
	}
	api := &parserpkg.ParsedAPI{
		Messages: map[string]*parserpkg.MessageType{
			mt.FullName:    mt,
			inner.FullName: inner,
		},
		Events: []*parserpkg.Event{{
			Message: mt, Subject: "all_scalars",
			Kind: parserpkg.EventKindBroadcast, GoPackage: "example.com/x",
		}},
	}
	doc := generateAsyncAPI(api.Events, api.Messages)
	for _, want := range []string{
		`"format": "int32"`, `"format": "int64"`,
		`"format": "uint32"`, `"format": "uint64"`,
		`"format": "float"`, `"format": "double"`,
		`"format": "byte"`, `"type": "boolean"`,
		`"type": "array"`,                      // repeated string
		`"red"`,                               // x_var_name aliased enum
		`"BLUE"`,                              // unaliased enum value
		`"title": "Thing"`,                     // external message stub
		`"v"`,                                 // nested inlined fields
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("AsyncAPI missing %q", want)
		}
	}

	// Sanity: the document is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("invalid AsyncAPI JSON: %v", err)
	}
}

// TestAsyncAPI_EmptyAndCycle covers the nil-message / no-fields / cycle
// paths in payloadSchema and inlineMessageSchema.
func TestAsyncAPI_EmptyAndCycle(t *testing.T) {
	if got := payloadSchema(nil, nil); got["type"] != "object" {
		t.Errorf("nil message: %v", got)
	}
	empty := &parserpkg.MessageType{Name: "Empty", FullName: ".x.Empty"}
	got := payloadSchema(empty, map[string]*parserpkg.MessageType{empty.FullName: empty})
	if got["type"] != "object" {
		t.Errorf("empty fields: %v", got)
	}
	// Self-referential message exercises the seen-set guard.
	self := &parserpkg.MessageType{
		Name: "Node", FullName: ".x.Node",
		Fields: []*parserpkg.Field{
			{Name: "child", Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, TypeName: ".x.Node"},
		},
	}
	idx := map[string]*parserpkg.MessageType{self.FullName: self}
	doc := payloadSchema(self, idx)
	if doc["type"] != "object" {
		t.Errorf("cycle should still produce a typed object root: %v", doc)
	}
}

func TestLastSegment(t *testing.T) {
	if got := lastSegment(".x.v1.Foo"); got != "Foo" {
		t.Errorf("got %q", got)
	}
	if got := lastSegment("Plain"); got != "Plain" {
		t.Errorf("got %q", got)
	}
}

// helpers shared with plugin_test.go
func sp(s string) *string { return &s }
func i32(v int32) *int32  { return &v }
