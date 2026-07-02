package ast

import (
	"strings"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		wantErr string // substring; "" means no error
	}{
		{"omitted is compatible", "", ""},
		{"major only", "1", ""},
		{"major.minor", "1.0", ""},
		{"full", "1.0.0", ""},
		{"too new (major)", "2", "needs a newer whoosh"},
		{"too new (patch)", "1.0.1", "needs a newer whoosh"},
		{"too old", "0.0.1", "no longer supported"},
		{"malformed", "not-a-version", "invalid version"},
	}
	for _, c := range cases {
		err := checkVersion(c.version)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: checkVersion(%q) = %v, want nil", c.name, c.version, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: checkVersion(%q) = %v, want error containing %q", c.name, c.version, err, c.wantErr)
		}
	}
}
