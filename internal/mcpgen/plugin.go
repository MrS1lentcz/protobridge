// Package mcpgen is the codegen backend for the protoc-gen-mcp plugin.
// It consumes the same internal/parser model as the REST proxy generator
// and emits an MCP proxy: main.go + handler/<service>.go.
package mcpgen

import (
	"fmt"
	"io"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/generator"
	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// Options carries plugin parameters parsed from `--mcp_opt=k=v`.
type Options struct {
	HandlerPkg string   // Go import path of the generated handler subpackage
	Forward    []string // identity keys forwarded by DefaultAuthFunc; defaults to ["session_id"]
}

// ParseOptions reads the `--mcp_opt=...` parameter string. Format mirrors
// the REST plugin: comma-separated key=value pairs.
func ParseOptions(raw string) (Options, error) {
	opts := Options{Forward: []string{"session_id"}}
	if raw == "" {
		return opts, nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return opts, fmt.Errorf("invalid plugin option %q: expected key=value", part)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "handler_pkg":
			opts.HandlerPkg = v
		case "forward":
			// Comma-separated would conflict with our top-level separator,
			// so accept semicolons here: forward=session_id;auth_token
			opts.Forward = nil
			for _, f := range strings.Split(v, ";") {
				f = strings.TrimSpace(f)
				if f != "" {
					opts.Forward = append(opts.Forward, f)
				}
			}
		default:
			return opts, fmt.Errorf("unknown plugin option %q", k)
		}
	}
	return opts, nil
}

// Run reads the CodeGeneratorRequest, parses options + proto, and emits the
// MCP proxy files. Errors are returned via CodeGeneratorResponse.Error per
// the protoc plugin contract.
func Run(r io.Reader) *pluginpb.CodeGeneratorResponse {
	data, err := io.ReadAll(r)
	if err != nil {
		return errResponse(err)
	}
	var req pluginpb.CodeGeneratorRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return errResponse(err)
	}
	opts, err := ParseOptions(req.GetParameter())
	if err != nil {
		return errResponse(err)
	}
	api, err := parser.Parse(&req)
	if err != nil {
		return errResponse(err)
	}
	resp, err := Generate(api, opts)
	if err != nil {
		return errResponse(err)
	}
	return resp
}

// Generate produces the MCP proxy files for an api.
func Generate(api *parser.ParsedAPI, opts Options) (*pluginpb.CodeGeneratorResponse, error) {
	// Reuse the REST plugin's handler-package resolver: same hybrid contract
	// (explicit param + go.mod walk + conventional dir).
	handlerPkg, err := generator.ResolveHandlerPkg(generator.Options{HandlerPkg: opts.HandlerPkg})
	if err != nil {
		return nil, err
	}

	resp := &pluginpb.CodeGeneratorResponse{}

	for _, svc := range api.Services {
		if !serviceHasMCP(svc) {
			continue
		}
		content, err := generateHandlerFile(svc)
		if err != nil {
			return nil, fmt.Errorf("handler for %s: %w", svc.Name, err)
		}
		name := "handler/" + camelToSnake(svc.Name) + ".go"
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name: &name, Content: &content,
		})
	}

	mainContent, err := generateMain(api, handlerPkg, opts.Forward)
	if err != nil {
		return nil, err
	}
	mainName := "main.go"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name: &mainName, Content: &mainContent,
	})

	// Schema artifacts: openrpc.json for client codegen (openrpc-generator
	// produces TS/Go/Python wrappers), and mcp-tools.json as a cached
	// `tools/list` response for MCP-native introspection tools.
	openRPC := generateOpenRPC(api)
	openRPCName := "schema/openrpc.json"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name: &openRPCName, Content: &openRPC,
	})

	mcpTools := generateMCPTools(api)
	mcpToolsName := "schema/mcp-tools.json"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name: &mcpToolsName, Content: &mcpTools,
	})

	return resp, nil
}

func errResponse(err error) *pluginpb.CodeGeneratorResponse {
	msg := err.Error()
	return &pluginpb.CodeGeneratorResponse{Error: &msg}
}

// camelToSnake mirrors the snake_case used by the REST plugin for filenames.
func camelToSnake(s string) string { return toSnakeCase(s) }
