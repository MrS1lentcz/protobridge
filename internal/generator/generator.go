package generator

import (
	"fmt"

	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// Generate takes a ParsedAPI and produces a CodeGeneratorResponse with all
// generated Go source files and the OpenAPI spec.
func Generate(api *parser.ParsedAPI) (*pluginpb.CodeGeneratorResponse, error) {
	resp := &pluginpb.CodeGeneratorResponse{}

	// Generate a handler file for each service.
	for _, svc := range api.Services {
		content, err := generateServiceFile(svc, api)
		if err != nil {
			return nil, fmt.Errorf("generating %s: %w", svc.Name, err)
		}
		name := toSnakeCase(svc.Name) + ".go"
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name:    &name,
			Content: &content,
		})
	}

	// Generate main.go.
	mainContent, err := generateMain(api)
	if err != nil {
		return nil, fmt.Errorf("generating main.go: %w", err)
	}
	mainName := "main.go"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &mainName,
		Content: &mainContent,
	})

	// Generate OpenAPI spec.
	openapiContent := GenerateOpenAPI(api)
	openapiName := "openapi.yaml"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name:    &openapiName,
		Content: &openapiContent,
	})

	// Generate AsyncAPI spec for WebSocket endpoints (if any).
	asyncapiContent := GenerateAsyncAPI(api)
	if asyncapiContent != "" {
		asyncapiName := "asyncapi.yaml"
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

	return resp, nil
}
