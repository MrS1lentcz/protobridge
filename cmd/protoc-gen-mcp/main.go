// Command protoc-gen-mcp is the protoc plugin that generates an MCP proxy
// from .proto files annotated with (protobridge.mcp) = true.
//
// Usage:
//
//	protoc \
//	    --mcp_out=./gen/mcp \
//	    --mcp_opt=handler_pkg=github.com/you/myapp/gen/mcp/handler \
//	    -I . -I path/to/protobridge/proto \
//	    your/service.proto
//
// The plugin emits main.go + handler/<service>.go. main.go runs an MCP
// server (stdio by default, streamable HTTP via PROTOBRIDGE_MCP_TRANSPORT=http)
// that proxies tool calls to the gRPC backend.
package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/internal/mcpgen"
)

func main() {
	resp := mcpgen.Run(os.Stdin)
	out, err := proto.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "protoc-gen-mcp: marshal response: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "protoc-gen-mcp: write response: %v\n", err)
		os.Exit(1)
	}
}
