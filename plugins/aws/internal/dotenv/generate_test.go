package dotenv

import "testing"

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"DATABASE_URL":      "DATABASE_URL",
		"redis-url":         "REDIS_URL",
		"github-auth-key":   "GITHUB_AUTH_KEY",
		"my.app/key":        "MY_APP_KEY",
		"_leading.trailing": "LEADING_TRAILING",
		"":                  "_",
	}
	for in, want := range cases {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRender_Escaping(t *testing.T) {
	env := map[string]string{
		"A": `quote " and back\slash`,
		"B": "line1\nline2",
		"C": "",
		"D": `pa$$word ${REF}`,
	}
	// Escaped (single-line): newlines collapse to a literal \n; '$' is escaped so dotenv parsers don't interpolate it.
	if got, want := Render(env, false), `A="quote \" and back\\slash"`+"\n"+`B="line1\nline2"`+"\n"+"C=\n"+`D="pa\$\$word \${REF}"`+"\n"; got != want {
		t.Fatalf("Render(escaped):\n got: %q\nwant: %q", got, want)
	}
	// Multiline (default): real newlines are kept inside the quotes.
	if got, want := Render(env, true), `A="quote \" and back\\slash"`+"\n"+"B=\"line1\nline2\"\n"+"C=\n"+`D="pa\$\$word \${REF}"`+"\n"; got != want {
		t.Fatalf("Render(multiline):\n got: %q\nwant: %q", got, want)
	}
}

// A PEM key keeps its real newlines under the default (multiline) form, the shape the dotenv/Rails gems require.
func TestRender_MultilinePEM(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"
	got := Render(map[string]string{"PRIVATE_KEY": key}, true)
	want := "PRIVATE_KEY=\"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----\"\n"
	if got != want {
		t.Fatalf("PEM multiline:\n got: %q\nwant: %q", got, want)
	}
}
