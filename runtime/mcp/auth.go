// Package mcp is the runtime support library for protobridge-generated MCP
// proxies. The MCP plugin emits a thin main.go and per-service handler files
// that register tools against this package; all wire concerns
// (transport, auth, gRPC dispatch) live here.
package mcp

import (
	"context"
	"net/http"
	"os"
	"strings"

	"google.golang.org/grpc/metadata"
)

// ConnectionInfo gives an MCPAuthFunc a unified view of the originating
// connection regardless of transport:
//
//   - stdio: HTTPHeaders is nil, InitializeParams may be nil, Env is the
//     proxy process environment (parent typically passes auth tokens here).
//   - streamable HTTP: HTTPHeaders carries the Authorization header etc.;
//     InitializeParams holds the JSON-RPC initialize params if MCP client
//     supplied any.
//
// The MCPAuthFunc returns gRPC metadata that protobridge will attach to
// every backend call made on behalf of this connection.
type ConnectionInfo struct {
	Env              map[string]string
	HTTPHeaders      http.Header
	InitializeParams map[string]any
}

// MCPAuthFunc translates a per-connection identity (env vars, HTTP headers,
// or MCP initialize params) into gRPC metadata that the proxy will attach
// to every backend call. Returning an error fails the MCP request.
//
// Implementations should be cheap — they may run on every tool call when
// using HTTP transport without session persistence.
type MCPAuthFunc func(ctx context.Context, conn ConnectionInfo) (metadata.MD, error)

// DefaultAuthFunc forwards a configurable list of identity keys from env
// vars and HTTP headers into gRPC metadata, lowercasing keys per the gRPC
// metadata convention. Use it as a starting point — wrap it for projects
// that need DB lookups or token validation.
//
// Each key in `forward` is checked against (in order):
//  1. ConnectionInfo.HTTPHeaders[key]            (HTTP transport)
//  2. ConnectionInfo.InitializeParams[lowercase] (MCP initialize handshake)
//  3. ConnectionInfo.Env[snake-uppercase form]   (stdio transport)
//
// Example: forward=["session_id"] with the env var `SESSION_ID=abc` set
// produces metadata `session_id=abc`.
func DefaultAuthFunc(forward ...string) MCPAuthFunc {
	return func(_ context.Context, conn ConnectionInfo) (metadata.MD, error) {
		md := metadata.MD{}
		for _, key := range forward {
			if v := lookup(conn, key); v != "" {
				md.Set(strings.ToLower(key), v)
			}
		}
		return md, nil
	}
}

func lookup(conn ConnectionInfo, key string) string {
	if conn.HTTPHeaders != nil {
		if v := conn.HTTPHeaders.Get(key); v != "" {
			return v
		}
	}
	if conn.InitializeParams != nil {
		if v, ok := conn.InitializeParams[strings.ToLower(key)].(string); ok && v != "" {
			return v
		}
	}
	if conn.Env != nil {
		if v := conn.Env[envKey(key)]; v != "" {
			return v
		}
	}
	// Fallback to process env so generated main.go doesn't have to read os.Environ() itself.
	return os.Getenv(envKey(key))
}

// envKey converts a metadata-style key (e.g. "session_id") to the
// SCREAMING_SNAKE form most parents use for env vars (e.g. "SESSION_ID").
func envKey(key string) string {
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c-32)
		case c == '-':
			out = append(out, '_')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
