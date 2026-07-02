package scm

import (
	"embed"

	"github.com/yousysadmin/whoosh/internal/shtmpl"
)

//go:embed templates/*.sh.tmpl
var tmplFS embed.FS

// tmpls holds the parsed git command templates.
var tmpls = shtmpl.MustParseFS(tmplFS, "templates/*.sh.tmpl")
