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

func TestPackageFilenameStem_EdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com/foo/bar", "example_com_foo_bar"},
		{`a\b\c`, "a_b_c"},                  // Windows-style separators
		{"foo-bar.v1", "foo_bar_v1"},        // dashes + dots
		{"////", "events"},                  // collapses to empty → fallback stem
		{"", "events"},                      // empty input → fallback stem
		{"_leading_trailing_", "leading_trailing"}, // strips leading/trailing underscores
	}
	for _, tc := range cases {
		if got := packageFilenameStem(tc.in); got != tc.want {
			t.Errorf("packageFilenameStem(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
		// Full package path encoded into the stem so foo/v1 + bar/v1 cannot collide.
		{"example.com/foo/events", "events", "example_com_foo_events_events.go"},
		{"example.com/foo/myapp", "events", "example_com_foo_myapp_events.go"},
		// Custom output_pkg adds a directory level to keep the override
		// visible in the layout.
		{"example.com/foo/myapp", "eventspkg", "eventspkg/example_com_foo_myapp_events.go"},
		// Versioned packages with the same leaf land in distinct files.
		{"a.com/foo/v1", "events", "a_com_foo_v1_events.go"},
		{"a.com/bar/v1", "events", "a_com_bar_v1_events.go"},
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
	_ = generateEventsFile("example.com/x", "events", nil)
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

func TestBroadcastServiceFilename(t *testing.T) {
	cases := []struct {
		svcName, outputPkg, want string
	}{
		{"OrderBroadcast", "events", "order_broadcast_broadcast.go"},
		{"OrderBroadcast", "eventspkg", "eventspkg/order_broadcast_broadcast.go"},
	}
	for _, tc := range cases {
		if got := broadcastServiceFilename(tc.svcName, tc.outputPkg); got != tc.want {
			t.Errorf("broadcastServiceFilename(%q, %q) = %q, want %q", tc.svcName, tc.outputPkg, got, tc.want)
		}
	}
}

func TestGenerateServiceBroadcastFile_SinglePackage(t *testing.T) {
	mt := &parserpkg.MessageType{Name: "OrderCreated", FullName: ".myapp.OrderCreated"}
	envMt := &parserpkg.MessageType{Name: "OrderEnvelope", FullName: ".myapp.OrderEnvelope"}
	svc := &parserpkg.BroadcastService{
		Name:         "OrderBroadcast",
		MethodName:   "Stream",
		Route:        "/api/v1/events/orders",
		GoPackage:    "example.com/myapp",
		ProtoPackage: "myapp",
		Envelope:     envMt,
		Events: []*parserpkg.BroadcastEvent{{
			OneofFieldName: "order_created",
			Message:        mt,
			Subject:        "order_created",
			Visibility:     parserpkg.EventVisibilityPublic,
			GoPackage:      "example.com/myapp",
		}},
	}
	got := generateServiceBroadcastFile(svc, "events")
	for _, want := range []string{
		"OrderBroadcastRoute",
		"/api/v1/events/orders",
		"OrderBroadcastSubjects",
		"OrderBroadcastEnvelope",
		"NewOrderBroadcastSource",
		"NewOrderBroadcastServer",
		"RegisterOrderBroadcastBroadcast",
		`pb "example.com/myapp"`,
		"&pb.OrderCreated{}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated broadcast service file:\n%s", want, got)
		}
	}
}

func TestGenerateServiceBroadcastFile_MultiPackage(t *testing.T) {
	orderMt := &parserpkg.MessageType{Name: "OrderCreated", FullName: ".orders.OrderCreated"}
	shipMt := &parserpkg.MessageType{Name: "ShipmentDispatched", FullName: ".shipping.ShipmentDispatched"}
	envMt := &parserpkg.MessageType{Name: "LogisticsEnvelope", FullName: ".logistics.LogisticsEnvelope"}
	svc := &parserpkg.BroadcastService{
		Name:         "LogisticsBroadcast",
		MethodName:   "Stream",
		Route:        "/api/v1/logistics",
		GoPackage:    "example.com/orders",
		ProtoPackage: "logistics",
		Envelope:     envMt,
		Events: []*parserpkg.BroadcastEvent{
			{OneofFieldName: "order_created", Message: orderMt, Subject: "order_created", GoPackage: "example.com/orders"},
			{OneofFieldName: "shipment_dispatched", Message: shipMt, Subject: "shipment_dispatched", GoPackage: "example.com/shipping"},
		},
	}
	got := generateServiceBroadcastFile(svc, "events")
	// Multi-pkg → aliased imports, each message references its own alias.
	if !strings.Contains(got, `pb0 "example.com/orders"`) {
		t.Errorf("missing aliased import pb0:\n%s", got)
	}
	if !strings.Contains(got, `pb1 "example.com/shipping"`) {
		t.Errorf("missing aliased import pb1:\n%s", got)
	}
	if !strings.Contains(got, "&pb0.OrderCreated{}") {
		t.Errorf("OrderCreated should be resolved to pb0 alias:\n%s", got)
	}
	if !strings.Contains(got, "&pb1.ShipmentDispatched{}") {
		t.Errorf("ShipmentDispatched should be resolved to pb1 alias:\n%s", got)
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

func TestGenerate_IncludesBroadcastServices(t *testing.T) {
	mt := &parserpkg.MessageType{Name: "OrderCreated", FullName: ".myapp.OrderCreated"}
	api := &parserpkg.ParsedAPI{
		Messages: map[string]*parserpkg.MessageType{mt.FullName: mt},
		Events: []*parserpkg.Event{{
			Message: mt, Subject: "order_created",
			Kind: parserpkg.EventKindBroadcast, Visibility: parserpkg.EventVisibilityPublic,
			GoPackage: "example.com/myapp",
		}},
		BroadcastServices: []*parserpkg.BroadcastService{{
			Name:         "OrderBroadcast",
			MethodName:   "Stream",
			Route:        "/api/v1/events/orders",
			GoPackage:    "example.com/myapp",
			ProtoPackage: "myapp",
			Envelope:     &parserpkg.MessageType{Name: "OrderEnvelope", FullName: ".myapp.OrderEnvelope"},
			Events: []*parserpkg.BroadcastEvent{{
				OneofFieldName: "order_created",
				Message:        mt,
				Subject:        "order_created",
				GoPackage:      "example.com/myapp",
			}},
		}},
	}
	resp, err := Generate(api, Options{OutputPkg: "events"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range resp.File {
		if strings.Contains(f.GetName(), "order_broadcast_broadcast.go") {
			found = true
			if !strings.Contains(f.GetContent(), "RegisterOrderBroadcastBroadcast") {
				t.Errorf("broadcast file missing register symbol")
			}
		}
	}
	if !found {
		t.Fatalf("broadcast service file not emitted; got %d files", len(resp.File))
	}
}

func TestGenerateServiceBroadcastFile_EmptyGoPackageFallsBackToService(t *testing.T) {
	// When BroadcastEvent.GoPackage is empty, the generator falls back to
	// the service's GoPackage (covers both branches of the pkg-resolution
	// conditionals).
	mt := &parserpkg.MessageType{Name: "X", FullName: ".x.X"}
	svc := &parserpkg.BroadcastService{
		Name:       "XBroadcast",
		MethodName: "Stream",
		Route:      "/x",
		GoPackage:  "example.com/x",
		Envelope:   &parserpkg.MessageType{Name: "XEnvelope", FullName: ".x.XEnvelope"},
		Events: []*parserpkg.BroadcastEvent{{
			OneofFieldName: "x", Message: mt, Subject: "x",
			// GoPackage intentionally empty.
		}},
	}
	got := generateServiceBroadcastFile(svc, "events")
	if !strings.Contains(got, `pb "example.com/x"`) {
		t.Errorf("empty BroadcastEvent.GoPackage should fall back to service.GoPackage:\n%s", got)
	}
}

func TestGenerateServiceBroadcastFile_ServicePkgDisjointFromEventPkg(t *testing.T) {
	// Service's own GoPackage lives outside every event's GoPackage — the
	// generator must append a fresh import under the `svcpb` alias instead
	// of reusing an existing entry.
	orderMt := &parserpkg.MessageType{Name: "OrderCreated", FullName: ".orders.OrderCreated"}
	envMt := &parserpkg.MessageType{Name: "Envelope", FullName: ".gw.Envelope"}
	svc := &parserpkg.BroadcastService{
		Name:         "GatewayBroadcast",
		MethodName:   "Stream",
		Route:        "/gw",
		GoPackage:    "example.com/gw", // distinct from event GoPackages
		ProtoPackage: "gw",
		Envelope:     envMt,
		Events: []*parserpkg.BroadcastEvent{
			{OneofFieldName: "order_created", Message: orderMt, Subject: "order_created", GoPackage: "example.com/orders"},
		},
	}
	got := generateServiceBroadcastFile(svc, "events")
	// Must contain the svcpb alias import for the service's own GoPackage.
	if !strings.Contains(got, `svcpb "example.com/gw"`) {
		t.Errorf("expected svcpb-aliased import for service GoPackage, got:\n%s", got)
	}
	// Must also contain the event's own pb import.
	if !strings.Contains(got, `pb "example.com/orders"`) {
		t.Errorf("expected pb-aliased import for event GoPackage, got:\n%s", got)
	}
	// The service stubs should reference the svcpb alias.
	if !strings.Contains(got, "svcpb.NewGatewayBroadcastClient") {
		t.Errorf("expected svcpb.New<Svc>Client reference, got:\n%s", got)
	}
}

func TestGenerate_MultipleBroadcastServicesSortedDeterministically(t *testing.T) {
	a := &parserpkg.MessageType{Name: "A", FullName: ".x.A"}
	b := &parserpkg.MessageType{Name: "B", FullName: ".x.B"}
	api := &parserpkg.ParsedAPI{
		Messages: map[string]*parserpkg.MessageType{a.FullName: a, b.FullName: b},
		BroadcastServices: []*parserpkg.BroadcastService{
			{Name: "ZBroadcast", MethodName: "Stream", Route: "/z", GoPackage: "example.com/x", Envelope: &parserpkg.MessageType{Name: "ZEnvelope", FullName: ".x.ZEnvelope"}, Events: []*parserpkg.BroadcastEvent{{OneofFieldName: "b", Message: b, Subject: "b", GoPackage: "example.com/x"}}},
			{Name: "ABroadcast", MethodName: "Stream", Route: "/a", GoPackage: "example.com/x", Envelope: &parserpkg.MessageType{Name: "AEnvelope", FullName: ".x.AEnvelope"}, Events: []*parserpkg.BroadcastEvent{{OneofFieldName: "a", Message: a, Subject: "a", GoPackage: "example.com/x"}}},
		},
	}
	resp, err := Generate(api, Options{OutputPkg: "events"})
	if err != nil {
		t.Fatal(err)
	}
	// Collect broadcast-file names in emission order.
	var names []string
	for _, f := range resp.File {
		if strings.Contains(f.GetName(), "_broadcast.go") {
			names = append(names, f.GetName())
		}
	}
	if len(names) != 2 || names[0] > names[1] {
		t.Errorf("expected alphabetical order, got %v", names)
	}
}

func TestGoCamelCase(t *testing.T) {
	// Cases mirror google.golang.org/protobuf/internal/strs.GoCamelCase so a
	// drift from upstream protoc-gen-go is caught here instead of producing
	// generated code that doesn't compile.
	cases := map[string]string{
		"":                   "",
		"foo":                "Foo",
		"foo_bar":            "FooBar",
		"task_created_event": "TaskCreatedEvent",
		"a__b":               "A_B",          // empty segment keeps the underscore
		"_foo":               "XFoo",         // leading _ → X (Go can't start with _)
		"foo_":               "Foo_",         // trailing _ kept verbatim
		"foo.bar":            "FooBar",       // .lower elides the dot
		"foo.Bar":            "Foo_Bar",      // .upper preserves as _
		"http_url":           "HttpUrl",      // no initialism normalisation
		"field_1_name":       "Field_1Name",  // digit forms its own word
	}
	for in, want := range cases {
		if got := goCamelCase(in); got != want {
			t.Errorf("goCamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}
