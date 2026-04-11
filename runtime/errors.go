package runtime

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// APIError is the standard error response body.
type APIError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

// FieldError represents a validation violation on a specific field.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// WriteError writes a structured error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIError{
		Code:    code,
		Message: message,
	}); err != nil {
		// Client disconnected during error write – log, don't Sentry.
		logError(err)
	}
}

// WriteValidationError writes a 422 with field-level violations.
func WriteValidationError(w http.ResponseWriter, violations []FieldError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := json.NewEncoder(w).Encode(APIError{
		Code:    "VALIDATION_ERROR",
		Message: "request validation failed",
		Details: violations,
	}); err != nil {
		logError(err)
	}
}

// WriteAuthError writes a 401 error from an auth failure.
func WriteAuthError(w http.ResponseWriter, err error) {
	WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication failed")
}

// WriteGRPCError maps a gRPC error to an HTTP error response and reports
// server-side errors (5xx) to Sentry. Client errors (4xx) and transient
// errors are logged to stderr only.
func WriteGRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error reaching the proxy is unexpected → Sentry.
		reportError(err)
		WriteError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
		return
	}

	httpStatus := grpcToHTTPStatus(st.Code())
	if httpStatus >= 500 {
		// Server-side gRPC errors (Internal, Unknown, etc.) → Sentry.
		reportError(err)
	}

	WriteError(w, httpStatus, st.Code().String(), st.Message())
}

func grpcToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
