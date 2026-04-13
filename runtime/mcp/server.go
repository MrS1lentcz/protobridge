package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Server wraps an mcp-go server and centralises the auth/metadata pipeline
// so generated handler files only have to register tools.
type Server struct {
	inner *mcpserver.MCPServer
	auth  MCPAuthFunc
}

// NewServer constructs a Server with the given name/version and auth
// translator. The auth translator runs once per tool call and produces the
// gRPC metadata appended to every backend invocation.
func NewServer(name, version string, auth MCPAuthFunc) *Server {
	return &Server{
		inner: mcpserver.NewMCPServer(name, version),
		auth:  auth,
	}
}

// AddTool registers an MCP tool. The handler should perform the gRPC call
// and return a CallToolResult; use UnaryHandler to build a standard one.
func (s *Server) AddTool(name, description string, rawInputSchema json.RawMessage, handler mcpserver.ToolHandlerFunc) {
	tool := mcp.Tool{
		Name:           name,
		Description:    description,
		RawInputSchema: rawInputSchema,
	}
	s.inner.AddTool(tool, handler)
}

// Inner exposes the underlying mcp-go server for advanced use cases
// (custom middlewares, prompts, resources). Generated code should not need it.
func (s *Server) Inner() *mcpserver.MCPServer { return s.inner }

// AuthFunc returns the configured MCPAuthFunc. Generated dispatchers use
// it to build gRPC metadata for each tool call.
func (s *Server) AuthFunc() MCPAuthFunc { return s.auth }

// ServeStdio runs the server over stdio (newline-delimited JSON-RPC).
// This is the transport used by Claude Desktop and most local MCP clients.
// The provided ctx is propagated into every per-request context so generated
// dispatchers see cancellation when the parent process exits.
func (s *Server) ServeStdio(ctx context.Context) error {
	return mcpserver.ServeStdio(s.inner, mcpserver.WithStdioContextFunc(func(_ context.Context) context.Context {
		return ctx
	}))
}

// ServeStreamableHTTP runs the server over the streamable HTTP transport
// at the given address. Used for remote MCP clients and proxies behind
// reverse proxies.
func (s *Server) ServeStreamableHTTP(addr string) error {
	srv := mcpserver.NewStreamableHTTPServer(s.inner)
	return srv.Start(addr)
}

// ServeFromEnv selects the transport based on PROTOBRIDGE_MCP_TRANSPORT
// (`stdio` or `http`, default `stdio`) and PROTOBRIDGE_MCP_HTTP_ADDR
// (default `:8081`).
func (s *Server) ServeFromEnv(ctx context.Context) error {
	switch transport := strings.ToLower(os.Getenv("PROTOBRIDGE_MCP_TRANSPORT")); transport {
	case "", "stdio":
		return s.ServeStdio(ctx)
	case "http", "streamable", "streamable_http":
		addr := os.Getenv("PROTOBRIDGE_MCP_HTTP_ADDR")
		if addr == "" {
			addr = ":8081"
		}
		return s.ServeStreamableHTTP(addr)
	default:
		return fmt.Errorf("unknown PROTOBRIDGE_MCP_TRANSPORT %q (want: stdio | http)", transport)
	}
}

// HTTPHeadersFromContext extracts HTTP headers from the incoming MCP request
// when the transport is streamable HTTP. Returns nil for stdio. Generated
// dispatchers feed it into ConnectionInfo.HTTPHeaders before calling auth.
func HTTPHeadersFromContext(ctx context.Context) http.Header {
	// mcp-go currently does not expose a public accessor; keep the helper
	// so generated code has a single integration point we can wire later
	// without changing every emitted handler.
	return nil
}

// CallUnary is the generic dispatcher used by every generated tool handler.
// It decodes the MCP arguments JSON into reqMsg, runs the auth translator,
// attaches metadata to the outgoing context, calls invoke (which performs
// the typed gRPC call and returns the response message), then marshals
// that response back into a text-content CallToolResult.
//
// invoke takes the prepared context and the populated request message and
// returns the response message — never copy a proto.Message by value
// (they hold a Mutex), so we never write into a caller-supplied respMsg.
func (s *Server) CallUnary(
	ctx context.Context,
	req mcp.CallToolRequest,
	reqMsg proto.Message,
	invoke func(ctx context.Context, reqMsg proto.Message) (proto.Message, error),
) (*mcp.CallToolResult, error) {
	if raw := req.GetRawArguments(); raw != nil {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("encode arguments: %w", err)
		}
		if len(data) > 0 && string(data) != "null" {
			opts := protojson.UnmarshalOptions{DiscardUnknown: true}
			if err := opts.Unmarshal(data, reqMsg); err != nil {
				return nil, fmt.Errorf("decode arguments into %T: %w", reqMsg, err)
			}
		}
	}

	conn := ConnectionInfo{
		HTTPHeaders: HTTPHeadersFromContext(ctx),
	}
	md, err := s.auth(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("mcp auth: %w", err)
	}
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	respMsg, err := invoke(ctx, reqMsg)
	if err != nil {
		return nil, err
	}

	out, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(respMsg)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}
