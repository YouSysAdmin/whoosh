package shtmpl

import "testing"

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"foo":     `'foo'`,
		"foo bar": `'foo bar'`,
		"it's":    `'it'\''s'`,
		"$HOME":   `'$HOME'`, // single quotes keep it literal
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteDouble(t *testing.T) {
	cases := map[string]string{
		"foo":                      `"foo"`,
		"$HOME/.rbenv/shims:$PATH": `"$HOME/.rbenv/shims:$PATH"`, // $ left intact for expansion
		`say "hi"`:                 `"say \"hi\""`,
		"back`tick`":               "\"back\\`tick\\`\"",
		`a\b`:                      `"a\\b"`,
	}
	for in, want := range cases {
		if got := QuoteDouble(in); got != want {
			t.Errorf("QuoteDouble(%q) = %q, want %q", in, got, want)
		}
	}
}
