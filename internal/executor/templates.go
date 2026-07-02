package executor

import (
	"embed"

	"github.com/yousysadmin/whoosh/internal/shtmpl"
)

//go:embed templates/*.sh.tmpl
var tmplFS embed.FS

// shellTmpl holds the parsed templates for local/remote command wrapping.
var shellTmpl = shtmpl.MustParseFS(tmplFS, "templates/*.sh.tmpl")
