package eventsgen

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// Options carries plugin parameters parsed from `--events_opt=k=v[,k=v]`.
type Options struct {
	// OutputPkg is the Go package name to use in the generated events file.
	// If empty, derives "events" — generated files live alongside the proto's
	// regular .pb.go output and reuse its import path so callers write
	// `events.EmitFoo(...)` after a single import.
	OutputPkg string
}

// ParseOptions reads the protoc plugin parameter string. Comma-separated
// key=value pairs, same convention as the REST + MCP plugins.
func ParseOptions(raw string) (Options, error) {
	opts := Options{OutputPkg: "events"}
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
		case "output_pkg":
			opts.OutputPkg = v
		default:
			return opts, fmt.Errorf("unknown plugin option %q", k)
		}
	}
	return opts, nil
}

// Run wires the plugin protocol: read CodeGeneratorRequest from r, parse,
// generate, return CodeGeneratorResponse. Errors surface via response.Error
// per the protoc plugin contract.
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

// Generate emits one helper file per Go package that contains events, one
// broadcast-WS handler file per (protobridge.broadcast) service, plus a
// single AsyncAPI document covering the whole request.
func Generate(api *parser.ParsedAPI, opts Options) (*pluginpb.CodeGeneratorResponse, error) {
	resp := &pluginpb.CodeGeneratorResponse{}
	if len(api.Events) == 0 && len(api.BroadcastServices) == 0 {
		return resp, nil
	}

	// Group events by Go package — we emit one events.go file per package
	// so users get a single import and a flat namespace per proto package.
	byPkg := map[string][]*parser.Event{}
	for _, ev := range api.Events {
		byPkg[ev.GoPackage] = append(byPkg[ev.GoPackage], ev)
	}
	pkgs := make([]string, 0, len(byPkg))
	for pkg := range byPkg {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		content := generateEventsFile(pkg, opts.OutputPkg, byPkg[pkg])
		name := filename(pkg, opts.OutputPkg)
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name: &name, Content: &content,
		})
	}

	// Emit one broadcast handler file per (protobridge.broadcast) service.
	// Order by service name for deterministic output.
	services := append([]*parser.BroadcastService(nil), api.BroadcastServices...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, svc := range services {
		content := generateServiceBroadcastFile(svc, opts.OutputPkg)
		name := broadcastServiceFilename(svc.Name, opts.OutputPkg)
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name: &name, Content: &content,
		})
	}

	asyncAPI := generateAsyncAPI(api.Events, api.Messages)
	asyncAPIName := "schema/asyncapi.json"
	resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
		Name: &asyncAPIName, Content: &asyncAPI,
	})

	return resp, nil
}

// filename returns the path to the events.go file relative to protoc's
// --events_out directory. The full Go import path is encoded into the
// filename stem so a single request that contains multiple packages
// sharing the same leaf (`foo/v1` and `bar/v1` are both "v1") cannot
// produce colliding output files.
//
// Example:
//
//	pkg = "github.com/you/myapp/gen/foo/v1" →
//	    "github_com_you_myapp_gen_foo_v1_events.go"
//
// When outputPkg is set to anything other than the default, the file is
// emitted under that directory.
func filename(pkgPath, outputPkg string) string {
	stem := packageFilenameStem(pkgPath)
	if outputPkg != "" && outputPkg != "events" {
		return outputPkg + "/" + stem + "_events.go"
	}
	return stem + "_events.go"
}

// broadcastServiceFilename derives the output path for a per-service
// broadcast WS handler. The stem is the service name in snake_case so the
// file sits alphabetically alongside the events helpers.
func broadcastServiceFilename(svcName, outputPkg string) string {
	stem := toSnakeCase(svcName) + "_broadcast.go"
	if outputPkg != "" && outputPkg != "events" {
		return outputPkg + "/" + stem
	}
	return stem
}

// toSnakeCase mirrors the REST generator's helper but lives here to avoid
// a cross-package dep cycle (eventsgen already imports generator for
// RenderTemplate; the helper is tiny enough to duplicate).
func toSnakeCase(s string) string {
	var out []rune
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

// packageFilenameStem turns a Go package import path into a stable,
// filesystem-safe stem suitable for generated filenames. Slashes,
// backslashes, dots and dashes all collapse to underscores so two
// distinct packages can never share a stem.
func packageFilenameStem(pkgPath string) string {
	replacer := strings.NewReplacer("/", "_", `\`, "_", ".", "_", "-", "_")
	stem := strings.Trim(replacer.Replace(pkgPath), "_")
	if stem == "" {
		return "events"
	}
	return stem
}

func errResponse(err error) *pluginpb.CodeGeneratorResponse {
	msg := err.Error()
	return &pluginpb.CodeGeneratorResponse{Error: &msg}
}
