package executor

import "testing"

func TestWrapRemote(t *testing.T) {
	// Env exports are emitted in sorted key order. Expandable values are double quoted so they can reference other shell
	// vars, literal values and the dir are single quoted.
	env := Env{"B": {Value: "$HOME/2", Expand: true}, "A": {Value: "$(id) `id` $X"}}
	if got, want := wrapRemote("echo hi", "/tmp", env),
		`export A='$(id) `+"`id`"+` $X'; export B="$HOME/2"; cd '/tmp' && echo hi`; got != want {
		t.Errorf("wrapRemote:\n got: %q\nwant: %q", got, want)
	}

	if got, want := wrapRemote("echo hi", "", nil), `echo hi`; got != want {
		t.Errorf("wrapRemote bare = %q, want %q", got, want)
	}
}

func TestBuildScriptCommand(t *testing.T) {
	got := buildScriptCommand("/bin/bash", "ls", "", Env{"X": {Value: "y", Expand: true}, "Y": {Value: "it's"}})
	want := "export X=\"y\"\nexport Y='it'\\''s'\n/bin/bash <<'__WHOOSH_SCRIPT_EOF__'\nls\n__WHOOSH_SCRIPT_EOF__"
	if got != want {
		t.Errorf("buildScriptCommand:\n got: %q\nwant: %q", got, want)
	}

	// Default interpreter + dir, content already ends with a newline.
	got2 := buildScriptCommand("", "ls\n", "/tmp", nil)
	want2 := "cd '/tmp' && /bin/sh <<'__WHOOSH_SCRIPT_EOF__'\nls\n__WHOOSH_SCRIPT_EOF__"
	if got2 != want2 {
		t.Errorf("buildScriptCommand default:\n got: %q\nwant: %q", got2, want2)
	}
}
