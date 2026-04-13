package generator

import (
	"strings"
	"testing"
	"text/template"
)

// renderTemplate panics on any template-execute or gofmt failure (those
// branches exist as defensive code that never fires for valid templates,
// so we exercise them via deliberately broken inputs to keep coverage
// honest about which paths are reachable).

func TestRenderTemplate_HappyPath(t *testing.T) {
	tmpl := template.Must(template.New("ok").Parse(`package x; var y = {{.}}`))
	got := renderTemplate(tmpl, 42)
	if !strings.Contains(got, "var y = 42") {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestRenderTemplate_TemplateExecuteFailurePanics(t *testing.T) {
	// {{.Missing}} on an int triggers a runtime template error.
	tmpl := template.Must(template.New("bad").Parse(`{{.Missing}}`))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from broken template execute")
		} else if !strings.Contains(r.(string), "template execute") {
			t.Errorf("panic message should identify the failure stage: %v", r)
		}
	}()
	_ = renderTemplate(tmpl, 1)
}

func TestRenderTemplate_GofmtFailurePanics(t *testing.T) {
	// Template renders syntactically broken Go → gofmt rejects it → panic.
	tmpl := template.Must(template.New("bad").Parse(`this is not valid Go {{.}}`))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from gofmt failure")
		} else if !strings.Contains(r.(string), "gofmt") {
			t.Errorf("panic message should identify the failure stage: %v", r)
		}
	}()
	_ = renderTemplate(tmpl, "x")
}

func TestRenderFragment_HappyPathSkipsGofmt(t *testing.T) {
	// Fragment templates emit Go that's not a complete file (no `package`
	// clause) — renderFragment must NOT run gofmt over them.
	tmpl := template.Must(template.New("frag").Parse(`var x = {{.}}`))
	got := renderFragment(tmpl, 42)
	if got != "var x = 42" {
		t.Errorf("got %q", got)
	}
}

func TestRenderFragment_TemplateExecuteFailurePanics(t *testing.T) {
	tmpl := template.Must(template.New("bad").Parse(`{{.Missing}}`))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from broken fragment template")
		}
	}()
	_ = renderFragment(tmpl, 1)
}

func TestRenderTemplate_PublicAlias(t *testing.T) {
	// RenderTemplate is the export sibling plugins consume; cover it
	// alongside the unexported internal so a refactor that breaks one
	// is caught here too.
	tmpl := template.Must(template.New("ok").Parse(`package y`))
	if got := RenderTemplate(tmpl, nil); got != "package y\n" {
		t.Errorf("got %q", got)
	}
}
