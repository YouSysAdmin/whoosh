// Package shtmpl renders the embedded shell-command templates used by the deploy, scm, and executor packages.
// It provides the shell-quoting primitives (Quote / QuoteDouble) and exposes them to templates as the sh/dq functions,
// plus a small parse/render API, so those packages keep their shell snippets in *.sh.tmpl files instead of fmt.Sprintf
// string-building.
package shtmpl

import (
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

// FuncMap is the function set available to shell templates.
// "sh" quotes a value as one safe literal shell word, "dq" double-quotes a value while allowing shell expansion (for
// env values that should expand, e.g. "$HOME/bin:$PATH").
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"sh": Quote,
		"dq": QuoteDouble,
	}
}

// MustParseFS parses every template matching patterns from fsys, with the sh func and missingkey=error.
// It panics on a malformed template - intended for use at package init with embedded files.
func MustParseFS(fsys fs.FS, patterns ...string) *template.Template {
	return template.Must(
		template.New("shtmpl").Funcs(FuncMap()).Option("missingkey=error").ParseFS(fsys, patterns...),
	)
}

// MustRender executes the named template against data and returns the result.
// It panics on error: the templates are static and parsed at init and the data is caller-controlled, so an execute
// failure is a programming bug (caught by tests) - the runtime analogue of template.Must.
func MustRender(t *template.Template, name string, data any) string {
	var b strings.Builder
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		panic(fmt.Sprintf("shtmpl: render %q: %v", name, err))
	}
	// Trailing newlines are not part of it.
	return strings.TrimRight(b.String(), "\n")
}
