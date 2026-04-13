package generator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options carries plugin parameters parsed from `--protobridge_opt=k=v[,k=v]`.
type Options struct {
	// HandlerPkg is the full Go import path of the generated `handler/`
	// subpackage that main.go imports. Required, but resolveHandlerPkg
	// can auto-detect it for the common layouts.
	HandlerPkg string
}

// ParseOptions parses the protoc plugin parameter string. Format follows the
// protoc convention: comma-separated `key=value` pairs.
func ParseOptions(raw string) (Options, error) {
	var opts Options
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
		default:
			return opts, fmt.Errorf("unknown plugin option %q", k)
		}
	}
	return opts, nil
}

// resolveHandlerPkg returns the import path of the handler subpackage.
//
// Resolution order:
//  1. Explicit `--protobridge_opt=handler_pkg=<path>` — always honored.
//  2. Auto-detect: walk up from CWD to find go.mod, read its module path,
//     then look for a conventional output directory (gen/protobridge,
//     gen/protobridge-rest, protobridge) and use `<module>/<that>/handler`.
//  3. Error with a clear remediation message.
//
// The auto-detect step is best-effort. The recommended setup is to pass
// `handler_pkg` explicitly so codegen is reproducible regardless of CWD.
func resolveHandlerPkg(opts Options) (string, error) {
	if opts.HandlerPkg != "" {
		return opts.HandlerPkg, nil
	}
	module, modRoot, err := findGoModule(".")
	if err != nil {
		return "", fmt.Errorf("auto-detect handler_pkg: %w; pass --protobridge_opt=handler_pkg=YOUR_MODULE/path/to/handler", err)
	}
	for _, candidate := range conventionalOutputDirs {
		full := filepath.Join(modRoot, candidate)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			return joinImportPath(module, candidate, "handler"), nil
		}
	}
	return "", fmt.Errorf(
		"auto-detect handler_pkg: found go.mod (module %s) but no conventional output dir (%s); "+
			"pass --protobridge_opt=handler_pkg=%s/<your-output-dir>/handler",
		module, strings.Join(conventionalOutputDirs, ", "), module,
	)
}

// conventionalOutputDirs is the search list for auto-detect, in priority order.
var conventionalOutputDirs = []string{
	"gen/protobridge",
	"protobridge",
	"gen",
}

// findGoModule walks up from start until it finds a go.mod, returning the
// declared module path and the directory containing the go.mod.
func findGoModule(start string) (module, root string, err error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			module = parseModulePath(data)
			if module == "" {
				return "", "", fmt.Errorf("go.mod at %s has no module declaration", modPath)
			}
			return module, dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", errors.New("no go.mod found from CWD up to filesystem root")
		}
		dir = parent
	}
}

func parseModulePath(modContent []byte) string {
	for _, line := range strings.Split(string(modContent), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

func joinImportPath(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.ReplaceAll(p, `\`, "/"), "/")
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}
