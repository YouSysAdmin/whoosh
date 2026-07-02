package dotenv

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nonKey = regexp.MustCompile(`[^A-Za-z0-9_]`)

// NormalizeKey renders k as a dotenv key: uppercase, non-alnum -> '_', surrounding underscores trimmed (e.g.
// "github-auth-key" -> "GITHUB_AUTH_KEY"). An empty result becomes "_".
func NormalizeKey(k string) string {
	k = nonKey.ReplaceAllString(strings.ToUpper(k), "_")
	if k = strings.Trim(k, "_"); k == "" {
		return "_"
	}
	return k
}

// Render formats env as a sorted dotenv file.
// Non-empty values are double-quoted with backslashes, quotes, and '$' escaped - dotenv parsers (godotenv, the Rails
// dotenv gem) interpolate $VAR inside double quotes, so an unescaped '$' in a secret would be expanded when the app
// loads the file; both honor \$ as a literal. Empty values render as KEY=.
// With multiline set, real newlines are kept inside the quotes (the form dotenv/Rails need for PEM keys, certs, ...),
// otherwise they collapse to a literal \n (one line per entry).
func Render(env map[string]string, multiline bool) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := env[k]
		if v == "" {
			fmt.Fprintf(&b, "%s=\n", k)
			continue
		}
		v = strings.ReplaceAll(v, "\r\n", "\n")
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		v = strings.ReplaceAll(v, `$`, `\$`)
		if !multiline {
			v = strings.ReplaceAll(v, "\n", `\n`)
		}
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, v)
	}
	return b.String()
}
