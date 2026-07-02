package executor

import "testing"

func TestWrapRemote(t *testing.T) {
	// Env exports are emitted in sorted key order, double-quoted so values can reference other shell vars, the dir stays
	// single-quoted (literal).
	if got, want := wrapRemote("echo hi", "/tmp", map[string]string{"B": "2", "A": "1"}),
		`export A="1"; export B="2"; cd '/tmp' && echo hi`; got != want {
		t.Errorf("wrapRemote:\n got: %q\nwant: %q", got, want)
	}
	if got, want := wrapRemote("echo hi", "", nil), `echo hi`; got != want {
		t.Errorf("wrapRemote bare = %q, want %q", got, want)
	}
}

func TestBuildScriptCommand(t *testing.T) {
	got := buildScriptCommand("/bin/bash", "ls", "", map[string]string{"X": "y"})
	want := "export X=\"y\"\n/bin/bash <<'__WHOOSH_SCRIPT_EOF__'\nls\n__WHOOSH_SCRIPT_EOF__"
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
