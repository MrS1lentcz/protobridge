package mcp_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime/mcp"
)

func TestDefaultAuthFunc_HTTPHeader(t *testing.T) {
	auth := mcp.DefaultAuthFunc("session_id", "auth_token")
	headers := http.Header{}
	headers.Set("session_id", "abc")
	headers.Set("auth_token", "xyz")

	md, err := auth(context.Background(), mcp.ConnectionInfo{HTTPHeaders: headers})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := md.Get("session_id"); len(got) != 1 || got[0] != "abc" {
		t.Errorf("session_id metadata: %v", got)
	}
	if got := md.Get("auth_token"); len(got) != 1 || got[0] != "xyz" {
		t.Errorf("auth_token metadata: %v", got)
	}
}

func TestDefaultAuthFunc_InitializeParams(t *testing.T) {
	auth := mcp.DefaultAuthFunc("session_id")
	conn := mcp.ConnectionInfo{InitializeParams: map[string]any{"session_id": "from-init"}}
	md, _ := auth(context.Background(), conn)
	if got := md.Get("session_id"); len(got) != 1 || got[0] != "from-init" {
		t.Errorf("got %v", got)
	}
}

func TestDefaultAuthFunc_EnvFallback(t *testing.T) {
	// Used by stdio transport: parent process passes identity via env.
	t.Setenv("SESSION_ID", "env-value")
	auth := mcp.DefaultAuthFunc("session_id")
	md, _ := auth(context.Background(), mcp.ConnectionInfo{})
	if got := md.Get("session_id"); len(got) != 1 || got[0] != "env-value" {
		t.Errorf("got %v", got)
	}
}

func TestDefaultAuthFunc_HeaderWinsOverEnv(t *testing.T) {
	t.Setenv("SESSION_ID", "from-env")
	headers := http.Header{}
	headers.Set("session_id", "from-header")
	auth := mcp.DefaultAuthFunc("session_id")
	md, _ := auth(context.Background(), mcp.ConnectionInfo{HTTPHeaders: headers})
	if got := md.Get("session_id"); len(got) != 1 || got[0] != "from-header" {
		t.Errorf("got %v", got)
	}
}

func TestDefaultAuthFunc_NoIdentityResultsInEmptyMD(t *testing.T) {
	// Make sure stale env from prior tests doesn't bleed in.
	_ = os.Unsetenv("SESSION_ID")
	auth := mcp.DefaultAuthFunc("session_id")
	md, err := auth(context.Background(), mcp.ConnectionInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(md) != 0 {
		t.Errorf("expected empty metadata, got %v", md)
	}
}

func TestDefaultAuthFunc_HyphenatedKey(t *testing.T) {
	t.Setenv("X_AUTH_TOKEN", "tok")
	auth := mcp.DefaultAuthFunc("x-auth-token")
	md, _ := auth(context.Background(), mcp.ConnectionInfo{})
	if got := md.Get("x-auth-token"); len(got) != 1 || got[0] != "tok" {
		t.Errorf("got %v", got)
	}
}
