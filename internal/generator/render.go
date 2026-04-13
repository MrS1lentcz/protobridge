package generator

import (
	"bytes"
	"go/format"
	"text/template"
)

// renderTemplate executes tmpl against data, then runs gofmt on the output.
// Both steps are deterministic for the package-scoped templates we ship —
// failures here mean a template bug or a regression in the generator's own
// helpers, never an input problem. Panicking surfaces those bugs in tests
// instead of hiding them behind error-return paths that never fire in
// production but drag coverage down.
//
// Exported so internal/mcpgen and internal/eventsgen can opt in to the
// same convention if/when they share template-execution patterns.
func renderTemplate(tmpl *template.Template, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic("generator: template execute: " + err.Error())
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		panic("generator: gofmt generated source: " + err.Error() + "\n" + buf.String())
	}
	return string(out)
}

// RenderTemplate is the public entry point for sibling plugin packages
// that want to reuse the same render-or-panic convention.
func RenderTemplate(tmpl *template.Template, data any) string {
	return renderTemplate(tmpl, data)
}

// renderFragment executes tmpl against data WITHOUT running gofmt. Use it
// for templates that emit Go *fragments* (no `package` clause / imports)
// — e.g. WS handler bodies that get spliced into a larger file. format.Source
// would reject a fragment as invalid syntax and panic; the parent template
// will gofmt the whole assembled file once.
func renderFragment(tmpl *template.Template, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic("generator: template execute: " + err.Error())
	}
	return buf.String()
}
