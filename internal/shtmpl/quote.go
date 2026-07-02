package shtmpl

import "strings"

// Quote wraps s in single quotes, escaping each embedded single quote (close, backslash-escape, reopen) so it is safe
// as one shell word. Exposed to templates as "sh".
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// doubleQuoteEscaper escapes the characters that are special inside shell double quotes - backslash, double-quote, and
// backtick - while deliberately leaving $ alone so parameter expansion still happens.
var doubleQuoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")

// QuoteDouble wraps s in double quotes for use as one shell word while still allowing parameter expansion ($VAR,
// ${VAR}).
// Use it for env values like "$HOME/.rbenv/shims:$PATH" that must expand at run time, use Quote for values that must
// stay literal. Exposed to templates as "dq".
func QuoteDouble(s string) string {
	return `"` + doubleQuoteEscaper.Replace(s) + `"`
}
