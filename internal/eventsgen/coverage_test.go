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

func TestRun_ReadError(t *testing.T) {
	resp := Run(&errReader{})
	if resp.Error == nil {
		t.Fatal("expected error response from failing reader")
	}
}

type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) { return 0, errStr("read failed") }

type errStr string

func (e errStr) Error() string { return string(e) }

func TestRun_InvalidProtoBytesYieldsErrorResponse(t *testing.T) {
	resp := Run(bytes.NewReader([]byte("not a proto")))
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
}

func TestRun_ParserError(t *testing.T) {
	// CodeGeneratorRequest with two methods both marked auth_method =
	// parser.Parse rejects → resp.Error must be set.
	authOpts := &descriptorpb.MethodOptions{}
	proto.SetExtension(authOpts, optionspb.E_AuthMethod, true)
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"x.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    sp("x.proto"),
			Package: sp("x.v1"),
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: sp("Req")}, {Name: sp("Resp")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: sp("S"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{Name: sp("A"), InputType: sp(".x.v1.Req"), OutputType: sp(".x.v1.Resp"), Options: authOpts},
					{Name: sp("B"), InputType: sp(".x.v1.Req"), OutputType: sp(".x.v1.Resp"), Options: authOpts},
				},
			}},
		}},
	}
	data, _ := proto.Marshal(req)
	resp := Run(bytes.NewReader(data))
	if resp.Error == nil {
		t.Fatal("expected parser-error to surface in resp.Error")
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

func TestGenerateEventsFile_EmptyInputPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty events (caller invariant violation)")
		}
	}()
	_ = generateEventsFile("example.com/x", nil)
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
func TestAsyncAPI_RequiredFieldsListed(t *testing.T) {
	// Required fields in the proto end up in the AsyncAPI message payload's
	// required[] array — covers the conditional that only fires when the
	// request type has any required fields.
	mt := &parserpkg.MessageType{
		Name: "Req", FullName: ".x.Req",
		Fields: []*parserpkg.Field{
			{Name: "id", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING, Required: true},
			{Name: "note", Type: descriptorpb.FieldDescriptorProto_TYPE_STRING}, // not required
		},
	}
	api := &parserpkg.ParsedAPI{
		Messages: map[string]*parserpkg.MessageType{mt.FullName: mt},
		Events: []*parserpkg.Event{{
			Message: mt, Subject: "x.req", Kind: parserpkg.EventKindBroadcast,
			Visibility: parserpkg.EventVisibilityPublic, GoPackage: "example.com/x",
		}},
	}
	doc := generateAsyncAPI(api.Events, api.Messages)
	if !strings.Contains(doc, `"required": [`) {
		t.Errorf("required[] missing from AsyncAPI: %s", doc)
	}
	if !strings.Contains(doc, `"id"`) {
		t.Errorf("required field name missing")
	}
}

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

func TestTitleCaseLeaf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"myapp", "Myapp"},
		{"Already", "Already"},
		{"v1", "V1"},
	}
	for _, tc := range cases {
		if got := titleCaseLeaf(tc.in); got != tc.want {
			t.Errorf("titleCaseLeaf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBroadcastFilename(t *testing.T) {
	cases := []struct {
		pkgPath, outputPkg, want string
	}{
		{"example.com/foo/myapp", "events", "myapp_broadcast.go"},
		{"example.com/foo/myapp", "eventspkg", "eventspkg/myapp_broadcast.go"},
		{"single", "events", "single_broadcast.go"},
	}
	for _, tc := range cases {
		if got := broadcastFilename(tc.pkgPath, tc.outputPkg); got != tc.want {
			t.Errorf("broadcastFilename(%q, %q) = %q, want %q", tc.pkgPath, tc.outputPkg, got, tc.want)
		}
	}
}

func TestGenerateBroadcastFile_NoPublicEventsReturnsEmpty(t *testing.T) {
	// Pure DURABLE event — must not produce a broadcast file.
	api := &parserpkg.ParsedAPI{}
	mt := &parserpkg.MessageType{Name: "X", FullName: ".x.X"}
	api.Messages = map[string]*parserpkg.MessageType{mt.FullName: mt}
	api.Events = []*parserpkg.Event{{
		Message: mt, Subject: "x", Kind: parserpkg.EventKindDurable,
		Visibility: parserpkg.EventVisibilityInternal, GoPackage: "example.com/x",
	}}
	got := generateBroadcastFile("example.com/x", api.Events)
	if got != "" {
		t.Errorf("expected empty content for non-public-fan-out events, got: %s", got)
	}
}

func TestGenerateBroadcastFile_PublicFanOutEmitsExportedSymbols(t *testing.T) {
	mt := &parserpkg.MessageType{Name: "OrderCreated", FullName: ".x.OrderCreated"}
	events := []*parserpkg.Event{{
		Message: mt, Subject: "order_created",
		Kind: parserpkg.EventKindBroadcast, Visibility: parserpkg.EventVisibilityPublic,
		GoPackage: "example.com/myapp",
	}}
	got := generateBroadcastFile("example.com/myapp", events)
	for _, want := range []string{
		"MyappBroadcastSubjects",
		"MyappBroadcastEnvelope",
		"RegisterMyappBroadcast",
		`"order_created"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated broadcast file:\n%s", want, got)
		}
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
