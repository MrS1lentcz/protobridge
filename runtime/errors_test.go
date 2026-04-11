package runtime_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "missing field")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var apiErr runtime.APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if apiErr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("expected code INVALID_ARGUMENT, got %s", apiErr.Code)
	}
	if apiErr.Message != "missing field" {
		t.Fatalf("expected message 'missing field', got %s", apiErr.Message)
	}
}

func TestWriteValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	runtime.WriteValidationError(w, []runtime.FieldError{
		{Field: "name", Reason: "required"},
		{Field: "age", Reason: "required"},
	})

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}

	var apiErr runtime.APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(apiErr.Details) != 2 {
		t.Fatalf("expected 2 details, got %d", len(apiErr.Details))
	}
}

func TestWriteAuthError(t *testing.T) {
	w := httptest.NewRecorder()
	runtime.WriteAuthError(w, fmt.Errorf("token expired"))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var apiErr runtime.APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if apiErr.Code != "UNAUTHENTICATED" {
		t.Fatalf("expected code UNAUTHENTICATED, got %s", apiErr.Code)
	}
	if apiErr.Message != "authentication failed" {
		t.Fatalf("expected message 'authentication failed', got %s", apiErr.Message)
	}
}

func TestWriteGRPCError_NonGRPCError(t *testing.T) {
	w := httptest.NewRecorder()
	runtime.WriteGRPCError(w, fmt.Errorf("plain error"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	var apiErr runtime.APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if apiErr.Code != "INTERNAL" {
		t.Fatalf("expected code INTERNAL, got %s", apiErr.Code)
	}
}

func TestWriteGRPCError_Mapping(t *testing.T) {
	tests := []struct {
		code     codes.Code
		expected int
	}{
		{codes.InvalidArgument, 400},
		{codes.NotFound, 404},
		{codes.PermissionDenied, 403},
		{codes.Unauthenticated, 401},
		{codes.Unavailable, 503},
		{codes.Unimplemented, 501},
		{codes.DeadlineExceeded, 504},
		{codes.AlreadyExists, 409},
		{codes.ResourceExhausted, 429},
		{codes.Internal, 500},
		{codes.Unknown, 500},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			w := httptest.NewRecorder()
			err := status.Error(tt.code, "test error")
			runtime.WriteGRPCError(w, err)

			if w.Code != tt.expected {
				t.Fatalf("code %s: expected HTTP %d, got %d", tt.code, tt.expected, w.Code)
			}

			var apiErr runtime.APIError
			if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if apiErr.Code != tt.code.String() {
				t.Fatalf("expected gRPC code %s in body, got %s", tt.code, apiErr.Code)
			}
		})
	}
}
