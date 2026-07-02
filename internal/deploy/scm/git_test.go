package scm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCommands(t *testing.T) {
	g := Git{Repo: "git@x:r.git", Branch: "main"}

	// EnsureMirror only rewrites the mirror config (the lock-taking `set-url`) when the origin URL changed; the steady
	// state is just `remote update`, so a stale config.lock can't block a deploy.
	if got, want := g.EnsureMirror("/srv/app/repo"),
		"if [ -d '/srv/app/repo'/objects ]; then\n"+
			"  if [ \"$(git -C '/srv/app/repo' config --get remote.origin.url 2>/dev/null)\" != 'git@x:r.git' ]; then\n"+
			"    rm -f '/srv/app/repo'/config.lock\n"+
			"    git -C '/srv/app/repo' remote set-url origin 'git@x:r.git'\n"+
			"  fi\n"+
			"  git -C '/srv/app/repo' remote update --prune\n"+
			"else\n"+
			"  git clone --mirror 'git@x:r.git' '/srv/app/repo'\n"+
			"fi"; got != want {
		t.Errorf("EnsureMirror:\n got: %q\nwant: %q", got, want)
	}
	if got, want := g.CreateRelease("/srv/app/repo", "/srv/app/releases/123"),
		`mkdir -p '/srv/app/releases/123' && git -C '/srv/app/repo' archive 'main' | tar -x -f - -C '/srv/app/releases/123'`; got != want {
		t.Errorf("CreateRelease:\n got: %q\nwant: %q", got, want)
	}
	if got, want := g.Revision("/srv/app/repo"), `git -C '/srv/app/repo' rev-parse 'main'`; got != want {
		t.Errorf("Revision = %q, want %q", got, want)
	}
}

// EnsureMirror must not fail on a stale config.lock: with the URL unchanged it skips the config-writing set-url
// entirely (so the leftover lock is irrelevant), and with the URL changed it clears the stale lock before set-url.
// Exercised against a real git mirror.
func TestEnsureMirror_StaleConfigLock(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// Isolate from the test machine's global/system git config; supply an identity so commits work without one.
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(wd, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = wd, env
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, e, out)
		}
	}
	mkRepo := func(name string) string {
		p := filepath.Join(dir, name)
		if e := os.MkdirAll(p, 0o755); e != nil {
			t.Fatal(e)
		}
		run(p, gitBin, "init", "-q")
		if e := os.WriteFile(filepath.Join(p, "f"), []byte(name), 0o644); e != nil {
			t.Fatal(e)
		}
		run(p, gitBin, "add", "f")
		run(p, gitBin, "commit", "-q", "-m", "c")
		return p
	}

	src := mkRepo("src")
	mirror := filepath.Join(dir, "repo")
	run(dir, gitBin, "clone", "--mirror", "-q", src, mirror)
	// A stale lock left by a previously-killed git.
	if e := os.WriteFile(filepath.Join(mirror, "config.lock"), nil, 0o644); e != nil {
		t.Fatal(e)
	}

	runEnsure := func(url string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", Git{Repo: url, Branch: "main"}.EnsureMirror(mirror))
		cmd.Env = env
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("EnsureMirror(%s) with stale lock: %v\n%s", url, e, out)
		}
	}

	// URL unchanged -> skips set-url, so the stale lock is never touched -> succeeds.
	runEnsure(src)

	// URL changed -> clears the stale lock, set-url, updates from the new source.
	src2 := mkRepo("src2")
	runEnsure(src2)
	out, e := exec.Command(gitBin, "-C", mirror, "config", "--get", "remote.origin.url").Output()
	if e != nil {
		t.Fatal(e)
	}
	if got := strings.TrimSpace(string(out)); got != src2 {
		t.Errorf("origin url = %q, want %q (set-url should have run)", got, src2)
	}
}
