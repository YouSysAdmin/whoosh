package masking

import (
	"bytes"
	"strings"
	"testing"
)

func TestString_Redacts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone string // substring that must NOT remain
		keep string // substring that must remain (context preserved); "" to skip
	}{
		{
			name: "gem source url with github token",
			in:   "gem source: https://eduvo:ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123@rubygems.pkg.github.com/eduvo/ already present in the cache",
			gone: "ghp_5zsMYN",
			keep: "https://eduvo:" + Placeholder + "@rubygems.pkg.github.com",
		},
		{
			name: "bare github token",
			in:   "token ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123 used",
			gone: "ghp_5zsMYN",
			keep: "token " + Placeholder + " used",
		},
		{
			name: "aws access key id",
			in:   "key=AKIAIOSFODNN7EXAMPLE done",
			gone: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name: "aws secret keeps key name",
			in:   "aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			gone: "wJalrXUtnFEMI",
			keep: "aws_secret_access_key=" + Placeholder,
		},
		{
			name: "generic password",
			in:   "DB_PASSWORD=sup3rs3cretvalue",
			gone: "sup3rs3cretvalue",
		},
		{
			name: "non-secret untouched",
			in:   "Bundle complete! 42 gems installed",
			gone: Placeholder,
			keep: "Bundle complete! 42 gems installed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := String(c.in)
			if c.gone != "" && strings.Contains(got, c.gone) {
				t.Errorf("secret leaked: %q still contains %q", got, c.gone)
			}
			if c.keep != "" && !strings.Contains(got, c.keep) {
				t.Errorf("expected %q to contain %q", got, c.keep)
			}
		})
	}
}

func TestDisabled_PassesThrough(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	secret := "token ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123"
	if got := String(secret); got != secret {
		t.Errorf("disabled String altered input: %q", got)
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if _, err := w.Write([]byte(secret + "\n")); err != nil {
		t.Fatal(err)
	}
	_ = w.Flush()
	if got := buf.String(); got != secret+"\n" {
		t.Errorf("disabled Writer altered output: %q", got)
	}
}

// TestWriter_RedactsAcrossWrites guards a secret split across two Write calls (e.g. host prefix written separately from
// the line body, as prefixWriter does).
func TestWriter_RedactsAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	_, _ = w.Write([]byte("[10.4.20.66] gem source: https://eduvo:ghp_5zsMYN"))
	_, _ = w.Write([]byte("abcdefghijklmnopqrstuvwxyz0123@rubygems.pkg.github.com/\n"))
	// A trailing partial line is only emitted on Flush.
	_, _ = w.Write([]byte("password=hunter2trailing"))

	if strings.Contains(buf.String(), "ghp_5zsMYN") {
		t.Errorf("token leaked before flush: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "[10.4.20.66] gem source: https://eduvo:"+Placeholder+"@") {
		t.Errorf("line not redacted/prefixed as expected: %q", buf.String())
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if strings.Contains(buf.String(), "hunter2trailing") {
		t.Errorf("trailing secret leaked: %q", buf.String())
	}
}

// resetSecrets clears registered literal secrets between tests (same package).
func resetSecrets() {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	secrets = nil
	secretSet = map[string]bool{}
}

// AddSecret masks a registered literal everywhere, even when no pattern matches.
func TestAddSecret_MasksLiteral(t *testing.T) {
	resetSecrets()
	defer resetSecrets()

	// A value that no built-in rule recognizes (not a known token format).
	const secret = "s0me-Custom_Value-XYZ"
	if got := String("token is " + secret + " ok"); !strings.Contains(got, secret) {
		t.Fatalf("unregistered value should pass through: %q", got)
	}
	AddSecret(secret)
	got := String("token is " + secret + " ok")
	if strings.Contains(got, secret) {
		t.Errorf("registered secret leaked: %q", got)
	}
	if !strings.Contains(got, Placeholder) {
		t.Errorf("expected placeholder, got %q", got)
	}
}

// Trivially short values are ignored so they can't blank out unrelated text.
func TestAddSecret_IgnoresShort(t *testing.T) {
	resetSecrets()
	defer resetSecrets()

	AddSecret("ab") // below minSecretLen
	AddSecret("")
	AddSecret("   ")
	if got := String("ab cd ab"); got != "ab cd ab" {
		t.Errorf("short/blank values should not be masked: %q", got)
	}
}

// With redaction disabled, registered secrets pass through (debug shows all).
func TestAddSecret_DisabledPassesThrough(t *testing.T) {
	resetSecrets()
	defer resetSecrets()
	AddSecret("super-secret-token")

	SetEnabled(false)
	defer SetEnabled(true)
	if got := String("v=super-secret-token"); got != "v=super-secret-token" {
		t.Errorf("disabled redaction should pass through: %q", got)
	}
}
