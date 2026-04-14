package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOptions_Empty(t *testing.T) {
	opts, err := ParseOptions("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.HandlerPkg != "" {
		t.Errorf("expected empty HandlerPkg, got %q", opts.HandlerPkg)
	}
}

func TestParseOptions_HandlerPkg(t *testing.T) {
	opts, err := ParseOptions("handler_pkg=github.com/foo/bar/gen/protobridge/handler")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.HandlerPkg != "github.com/foo/bar/gen/protobridge/handler" {
		t.Errorf("got %q", opts.HandlerPkg)
	}
}

func TestParseOptions_MultipleAndWhitespace(t *testing.T) {
	opts, err := ParseOptions(" handler_pkg=github.com/foo/bar/handler , ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.HandlerPkg != "github.com/foo/bar/handler" {
		t.Errorf("got %q", opts.HandlerPkg)
	}
}

func TestParseOptions_InvalidPair(t *testing.T) {
	if _, err := ParseOptions("handler_pkg"); err == nil {
		t.Error("expected error for missing =value")
	}
}

func TestParseOptions_UnknownKey(t *testing.T) {
	if _, err := ParseOptions("bogus=x"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestResolveHandlerPkg_ExplicitParamWins(t *testing.T) {
	got, err := resolveHandlerPkg(Options{HandlerPkg: "github.com/x/y/handler"}, "--protobridge_opt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "github.com/x/y/handler" {
		t.Errorf("got %q", got)
	}
}

func TestResolveHandlerPkg_AutoDetectFromGoMod(t *testing.T) {
	// Create a temp module with the conventional gen/protobridge layout.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/myapp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gen", "protobridge"), 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	got, err := resolveHandlerPkg(Options{}, "--protobridge_opt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "example.com/myapp/gen/protobridge/handler" {
		t.Errorf("got %q, want example.com/myapp/gen/protobridge/handler", got)
	}
}

func TestResolveHandlerPkg_AutoDetectFromNestedDir(t *testing.T) {
	// go.mod at root, CWD several levels deep — walk-up must find it.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/myapp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "deeply", "nested", "subdir")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "protobridge"), 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}

	got, err := resolveHandlerPkg(Options{}, "--protobridge_opt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "example.com/myapp/protobridge/handler" {
		t.Errorf("got %q", got)
	}
}

func TestResolveHandlerPkg_NoGoModError(t *testing.T) {
	root := t.TempDir() // empty — no go.mod
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	_, err := resolveHandlerPkg(Options{}, "--protobridge_opt")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "handler_pkg=") {
		t.Errorf("error should mention the override flag; got: %v", err)
	}
}

func TestResolveHandlerPkg_GoModButNoConventionalDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/myapp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	_, err := resolveHandlerPkg(Options{}, "--protobridge_opt")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "example.com/myapp") {
		t.Errorf("error should include detected module path for the user; got: %v", err)
	}
}

func TestResolveHandlerPkg_PublicAlias(t *testing.T) {
	// The public ResolveHandlerPkg alias is what mcpgen consumes; cover it
	// directly so a refactor that breaks the wrapper is caught.
	got, err := ResolveHandlerPkg(Options{HandlerPkg: "github.com/x/y/handler"}, "--mcp_opt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "github.com/x/y/handler" {
		t.Errorf("got %q", got)
	}
}

func TestFindGoModule_GoModWithNoModuleDeclaration(t *testing.T) {
	// A go.mod missing the `module` line should produce a clear error
	// instead of silently returning an empty path that breaks codegen later.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("go 1.22\n\n// no module line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := findGoModule(root)
	if err == nil {
		t.Fatal("expected error for go.mod without module declaration")
	}
	if !strings.Contains(err.Error(), "module declaration") {
		t.Errorf("error should mention missing module decl; got: %v", err)
	}
}

func TestParseModulePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"module example.com/foo\n", "example.com/foo"},
		{"// header\nmodule example.com/foo\n\ngo 1.22\n", "example.com/foo"},
		{"module \"example.com/foo\"\n", "example.com/foo"},
		{"go 1.22\n", ""},
	}
	for _, tc := range cases {
		if got := parseModulePath([]byte(tc.in)); got != tc.want {
			t.Errorf("parseModulePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJoinImportPath(t *testing.T) {
	if got := joinImportPath("example.com/foo", "/gen/protobridge/", "handler"); got != "example.com/foo/gen/protobridge/handler" {
		t.Errorf("got %q", got)
	}
	if got := joinImportPath("example.com/foo", "", "handler"); got != "example.com/foo/handler" {
		t.Errorf("got %q", got)
	}
}

func TestFindGoModule_NoGoModUpToRoot(t *testing.T) {
	// Starting from filesystem root walks once and immediately exhausts —
	// no go.mod can exist above /. Covers the loop terminal branch.
	if _, _, err := findGoModule("/"); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("expected no-go.mod error from /, got %v", err)
	}
}
