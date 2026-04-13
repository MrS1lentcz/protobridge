package generator

import (
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// Run reads a CodeGeneratorRequest from r, parses it, generates all output
// files, and returns the CodeGeneratorResponse. Errors are returned inside
// the response (not as Go errors), matching the protoc plugin contract.
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

func errResponse(err error) *pluginpb.CodeGeneratorResponse {
	msg := err.Error()
	return &pluginpb.CodeGeneratorResponse{Error: &msg}
}

// Generate takes a ParsedAPI and produces a CodeGeneratorResponse with all
// generated Go source files and the OpenAPI spec.
func Generate(api *parser.ParsedAPI, opts Options) (*pluginpb.CodeGeneratorResponse, error) {
	resp := &pluginpb.CodeGeneratorResponse{}

	handlerPkg, err := resolveHandlerPkg(opts, "--protobridge_opt")
	if err != nil {
		return nil, err
	}

	// Broadcast services require an explicit events_pkg so main.go can
	// import the generated New<Svc>Handler. Fail fast with a clear message
	// instead of emitting unbuildable code.
	if len(api.BroadcastServices) > 0 && opts.EventsPkg == "" {
		return nil, fmt.Errorf("api has %d (protobridge.broadcast) service(s) but events_pkg is not set; pass --protobridge_opt=events_pkg=YOUR_MODULE/path/to/events", len(api.BroadcastServices))
	}

	// Generate a handler file for each service. Files live under handler/
	// (separate Go package) so the proxy directory stays clean — only main.go
	// and deployment manifests sit at the root.
	for _, svc := range api.Services {
		content := generateServiceFile(svc, api)
		name := "handler/" + toSnakeCase(svc.Name) + ".go"
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name:    &name,
			Content: &content,
		})
	}

	// Generate main.go.
	mainContent := generateMain(api, handlerPkg, opts.EventsPkg)
	mainName := "main.go"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &mainName,
		Content: &mainContent,
	})

	// Generate OpenAPI spec.
	openapiContent := GenerateOpenAPI(api)
	openapiName := "schema/openapi.yaml"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &openapiName,
		Content: &openapiContent,
	})

	// Generate AsyncAPI spec for WebSocket endpoints (if any).
	asyncapiContent := GenerateAsyncAPI(api)
	if asyncapiContent != "" {
		asyncapiName := "schema/asyncapi.yaml"
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name:    &asyncapiName,
			Content: &asyncapiContent,
		})
	}

	// Generate Dockerfile.
	dockerContent := GenerateDockerfile()
	dockerName := "Dockerfile"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &dockerName,
		Content: &dockerContent,
	})

	// Generate Kubernetes manifest.
	k8sContent := GenerateK8sManifest(api)
	k8sName := "k8s.yaml"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &k8sName,
		Content: &k8sContent,
	})

	// Generate .env.example
	envExampleContent := GenerateEnvExample(api)
	envExampleName := ".env.example"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &envExampleName,
		Content: &envExampleContent,
	})

	// Generate .env.defaults
	envDefaultsContent := GenerateEnvDefaults(api)
	envDefaultsName := ".env.defaults"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &envDefaultsName,
		Content: &envDefaultsContent,
	})

	return resp, nil
}
