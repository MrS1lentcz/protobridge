package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	marshaller = protojson.MarshalOptions{
		EmitDefaultValues: false,
		UseProtoNames:     true,
	}
	unmarshaller = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

// DecodeRequest reads the HTTP request body and unmarshals it into a proto message.
func DecodeRequest(r *http.Request, msg proto.Message) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("reading request body: %w", err)
	}
	if err := UnmarshalProto(body, msg); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// UnmarshalProto decodes JSON bytes into a proto message using the runtime's
// standard options: x_var_name enum aliases are rewritten to canonical names
// and unknown fields are discarded. Non-HTTP transports (MCP, WebSocket) use
// this so they share decoding semantics with the REST gateway.
//
// Empty / whitespace-only / `null` input leaves msg untouched and returns nil.
// HTTP bodies routinely carry trailing newlines and some clients pad bare
// `null` with whitespace, so the no-op check must be whitespace-tolerant —
// bytes.TrimSpace allocates nothing when there is nothing to trim.
func UnmarshalProto(data []byte, msg proto.Message) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if rewritten, perr := preprocessEnumAliases(data, msg); perr == nil {
		data = rewritten
	}
	return unmarshaller.Unmarshal(data, msg)
}

// MarshalProto marshals a proto message to JSON using the runtime's standard
// options and applies x_var_name aliases on enum-typed fields. All response
// paths (unary, oneof discriminator, SSE/WS streaming) must go through this
// helper so JSON output is consistent regardless of transport.
func MarshalProto(msg proto.Message) ([]byte, error) {
	data, err := marshaller.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return postprocessEnumAliases(data, msg), nil
}

// WriteResponse marshals a proto message and writes it as JSON.
func WriteResponse(w http.ResponseWriter, status int, msg proto.Message) {
	data, err := MarshalProto(msg)
	if err != nil {
		// Proto3 marshal failure = data corruption → Sentry.
		reportError(err)
		WriteError(w, http.StatusInternalServerError, "INTERNAL", "failed to marshal response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		// Client disconnected during write – normal, log only.
		logError(err)
	}
}

// OneofRegistry maps oneof discriminator type names to field descriptors.
// Populated by generated code at init time.
type OneofRegistry struct {
	// TypeToField maps "ImageMessage" → field name "image_message" in the parent message.
	TypeToField map[string]string
}

// DiscriminatorField is the JSON field name used to identify oneof message
// variants. This field is reserved -- proto messages used inside oneof blocks
// must not define a field with this name. The "protobridge_" prefix ensures
// no collision with user-defined fields.
const DiscriminatorField = "protobridge_disc"

// MarshalOneofField marshals a oneof message variant with a discriminator field.
func MarshalOneofField(msg proto.Message, typeName string) (json.RawMessage, error) {
	data, err := MarshalProto(msg)
	if err != nil {
		return nil, err
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	typeBytes, _ := json.Marshal(typeName)
	m[DiscriminatorField] = typeBytes

	return json.Marshal(m)
}

// UnmarshalOneofField reads the discriminator from raw JSON and returns
// the type name so the caller can select the correct proto message type.
func UnmarshalOneofField(data json.RawMessage) (typeName string, err error) {
	var peek map[string]json.RawMessage
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", fmt.Errorf("reading oneof discriminator: %w", err)
	}
	raw, ok := peek[DiscriminatorField]
	if !ok {
		return "", fmt.Errorf("missing '%s' field in oneof object", DiscriminatorField)
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return "", fmt.Errorf("invalid '%s' value: %w", DiscriminatorField, err)
	}
	if name == "" {
		return "", fmt.Errorf("empty '%s' field in oneof object", DiscriminatorField)
	}
	return name, nil
}
