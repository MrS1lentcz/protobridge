package runtime_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestDecodeRequest_ValidJSON(t *testing.T) {
	body := `{"name":"alice","age":30,"status":"STATUS_ACTIVE"}`
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))

	msg := &pb.SimpleRequest{}
	if err := runtime.DecodeRequest(r, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Name != "alice" {
		t.Fatalf("expected name 'alice', got %q", msg.Name)
	}
	if msg.Age != 30 {
		t.Fatalf("expected age 30, got %d", msg.Age)
	}
	if msg.Status != pb.Status_STATUS_ACTIVE {
		t.Fatalf("expected STATUS_ACTIVE, got %v", msg.Status)
	}
}

func TestDecodeRequest_EmptyBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(""))

	msg := &pb.SimpleRequest{}
	if err := runtime.DecodeRequest(r, msg); err != nil {
		t.Fatalf("empty body should not error: %v", err)
	}
}

func TestDecodeRequest_InvalidJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{invalid"))

	msg := &pb.SimpleRequest{}
	if err := runtime.DecodeRequest(r, msg); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDecodeRequest_EnumOmitted_DefaultsToZero(t *testing.T) {
	body := `{"name":"alice","age":30}`
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))

	msg := &pb.SimpleRequest{}
	if err := runtime.DecodeRequest(r, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Status != pb.Status_STATUS_UNSPECIFIED {
		t.Fatalf("expected STATUS_UNSPECIFIED (0) for omitted enum, got %v", msg.Status)
	}
}

func TestWriteResponse_OmitsEnumZero(t *testing.T) {
	w := httptest.NewRecorder()
	msg := &pb.SimpleResponse{
		Id:     "123",
		Name:   "alice",
		Status: pb.Status_STATUS_UNSPECIFIED, // 0 value – should be omitted
	}
	runtime.WriteResponse(w, http.StatusOK, msg)

	body := w.Body.String()
	if bytes.Contains([]byte(body), []byte("status")) {
		t.Fatalf("expected enum 0 to be omitted from response, got: %s", body)
	}
}

func TestWriteResponse_IncludesNonZeroEnum(t *testing.T) {
	w := httptest.NewRecorder()
	msg := &pb.SimpleResponse{
		Id:     "123",
		Name:   "alice",
		Status: pb.Status_STATUS_ACTIVE,
	}
	runtime.WriteResponse(w, http.StatusOK, msg)

	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("STATUS_ACTIVE")) {
		t.Fatalf("expected STATUS_ACTIVE in response, got: %s", body)
	}
}

func TestMarshalOneofField_AddsDiscriminator(t *testing.T) {
	msg := &pb.TextContent{Body: "hello world"}
	data, err := runtime.MarshalOneofField(msg, "TextContent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	raw, ok := m[runtime.DiscriminatorField]
	if !ok {
		t.Fatalf("expected %s field in output", runtime.DiscriminatorField)
	}

	var disc string
	if err := json.Unmarshal(raw, &disc); err != nil {
		t.Fatalf("unmarshal disc error: %v", err)
	}
	if disc != "TextContent" {
		t.Fatalf("expected discriminator 'TextContent', got %q", disc)
	}

	// Verify original field is present.
	raw, ok = m["body"]
	if !ok {
		t.Fatal("expected body field in output")
	}
}

func TestUnmarshalOneofField_ReadsDiscriminator(t *testing.T) {
	input := []byte(`{"body":"hello","protobridge_disc":"TextContent"}`)
	typeName, err := runtime.UnmarshalOneofField(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typeName != "TextContent" {
		t.Fatalf("expected 'TextContent', got %q", typeName)
	}
}

func TestUnmarshalOneofField_MissingDiscriminator(t *testing.T) {
	input := []byte(`{"body":"hello"}`)
	_, err := runtime.UnmarshalOneofField(input)
	if err == nil {
		t.Fatal("expected error for missing discriminator")
	}
}

func TestUnmarshalOneofField_EmptyDiscriminator(t *testing.T) {
	input := []byte(`{"body":"hello","protobridge_disc":""}`)
	_, err := runtime.UnmarshalOneofField(input)
	if err == nil {
		t.Fatal("expected error for empty discriminator")
	}
}

func TestUnmarshalOneofField_InvalidJSON(t *testing.T) {
	input := []byte(`{invalid`)
	_, err := runtime.UnmarshalOneofField(input)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestUnmarshalOneofField_InvalidDiscriminatorType(t *testing.T) {
	input := []byte(`{"protobridge_disc": 123}`)
	_, err := runtime.UnmarshalOneofField(input)
	if err == nil {
		t.Fatal("expected error for non-string discriminator")
	}
}
