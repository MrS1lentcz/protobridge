package runtime_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestCORSConfigFromEnv_Defaults(t *testing.T) {
	// Clear all CORS env vars to ensure defaults.
	t.Setenv("PROTOBRIDGE_CORS_ORIGINS", "")
	t.Setenv("PROTOBRIDGE_CORS_METHODS", "")
	t.Setenv("PROTOBRIDGE_CORS_HEADERS", "")
	t.Setenv("PROTOBRIDGE_CORS_MAX_AGE", "")

	cfg := runtime.CORSConfigFromEnv()

	if len(cfg.AllowOrigins) != 1 || cfg.AllowOrigins[0] != "*" {
		t.Fatalf("expected default origins [*], got %v", cfg.AllowOrigins)
	}
	if len(cfg.AllowMethods) != 6 {
		t.Fatalf("expected 6 default methods, got %d", len(cfg.AllowMethods))
	}
	if len(cfg.AllowHeaders) != 2 {
		t.Fatalf("expected 2 default headers, got %d", len(cfg.AllowHeaders))
	}
	if cfg.MaxAge != 86400 {
		t.Fatalf("expected default max age 86400, got %d", cfg.MaxAge)
	}
}

func TestCORSConfigFromEnv_CustomOrigins(t *testing.T) {
	t.Setenv("PROTOBRIDGE_CORS_ORIGINS", "https://a.com, https://b.com")

	cfg := runtime.CORSConfigFromEnv()
	if len(cfg.AllowOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d: %v", len(cfg.AllowOrigins), cfg.AllowOrigins)
	}
	if cfg.AllowOrigins[0] != "https://a.com" || cfg.AllowOrigins[1] != "https://b.com" {
		t.Fatalf("unexpected origins: %v", cfg.AllowOrigins)
	}
}

func TestCORSConfigFromEnv_CustomMethods(t *testing.T) {
	t.Setenv("PROTOBRIDGE_CORS_METHODS", "GET,POST")

	cfg := runtime.CORSConfigFromEnv()
	if len(cfg.AllowMethods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(cfg.AllowMethods))
	}
}

func TestCORSConfigFromEnv_CustomHeaders(t *testing.T) {
	t.Setenv("PROTOBRIDGE_CORS_HEADERS", "X-Custom, Authorization")

	cfg := runtime.CORSConfigFromEnv()
	if len(cfg.AllowHeaders) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(cfg.AllowHeaders))
	}
	if cfg.AllowHeaders[0] != "X-Custom" {
		t.Fatalf("expected X-Custom, got %s", cfg.AllowHeaders[0])
	}
}

func TestCORSConfigFromEnv_CustomMaxAge(t *testing.T) {
	t.Setenv("PROTOBRIDGE_CORS_MAX_AGE", "3600")

	cfg := runtime.CORSConfigFromEnv()
	if cfg.MaxAge != 3600 {
		t.Fatalf("expected max age 3600, got %d", cfg.MaxAge)
	}
}

func TestCORSConfigFromEnv_InvalidMaxAge(t *testing.T) {
	t.Setenv("PROTOBRIDGE_CORS_MAX_AGE", "not-a-number")

	cfg := runtime.CORSConfigFromEnv()
	// Should keep default on parse error.
	if cfg.MaxAge != 86400 {
		t.Fatalf("expected default max age 86400 on invalid input, got %d", cfg.MaxAge)
	}
}

func TestCORSMiddleware_PreflightReturns204(t *testing.T) {
	cfg := runtime.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       3600,
	}

	handler := runtime.CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for preflight")
	}))

	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected Allow-Origin *, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("expected methods 'GET, POST', got %q", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Fatalf("expected max age 3600, got %q", got)
	}
}

func TestCORSMiddleware_RegularRequestGetsCORSHeaders(t *testing.T) {
	cfg := runtime.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       100,
	}

	nextCalled := false
	handler := runtime.CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if !nextCalled {
		t.Fatal("next handler should be called for non-OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected Allow-Origin *, got %q", got)
	}
}

func TestCORSMiddleware_NoOriginHeader_SkipsCORS(t *testing.T) {
	cfg := runtime.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       100,
	}

	nextCalled := false
	handler := runtime.CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Origin header.
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if !nextCalled {
		t.Fatal("next handler should be called when no Origin")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
}

func TestCORSMiddleware_SpecificOrigins_MatchingOrigin(t *testing.T) {
	cfg := runtime.CORSConfig{
		AllowOrigins: []string{"https://a.com", "https://b.com"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       100,
	}

	handler := runtime.CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://b.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://b.com" {
		t.Fatalf("expected Allow-Origin https://b.com, got %q", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary: Origin for non-wildcard, got %q", got)
	}
}

func TestCORSMiddleware_SpecificOrigins_NonMatchingOrigin(t *testing.T) {
	cfg := runtime.CORSConfig{
		AllowOrigins: []string{"https://a.com"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       100,
	}

	nextCalled := false
	handler := runtime.CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if !nextCalled {
		t.Fatal("next handler should still be called for non-matching origin")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header for non-matching origin, got %q", got)
	}
}
