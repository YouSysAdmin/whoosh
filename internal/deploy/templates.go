package deploy

import (
	"embed"

	"github.com/yousysadmin/whoosh/internal/shtmpl"
)

//go:embed templates/*.sh.tmpl
var tmplFS embed.FS

// tmpl holds the parsed shell templates for the deploy lifecycle commands.
var tmpl = shtmpl.MustParseFS(tmplFS, "templates/*.sh.tmpl")
