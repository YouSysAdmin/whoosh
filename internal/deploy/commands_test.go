package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/paths"
)

func TestPublishCmd(t *testing.T) {
	l := paths.For("/srv/app")
	got := publishCmd(l.Release("123"), l.CurrentPath)
	want := `rm -f '/srv/app/current.tmp'; ln -s '/srv/app/releases/123' '/srv/app/current.tmp'; mv -T '/srv/app/current.tmp' '/srv/app/current' 2>/dev/null || { rm -f '/srv/app/current'; mv '/srv/app/current.tmp' '/srv/app/current'; }`
	if got != want {
		t.Errorf("publishCmd:\n got: %q\nwant: %q", got, want)
	}
}

func TestEnsureStructureCmd(t *testing.T) {
	l := paths.For("/srv/app")
	got := ensureStructureCmd(l, []string{".env"}, []string{"log"})
	want := "set -e\n" +
		"mkdir -p '/srv/app/releases' '/srv/app/shared' '/srv/app/shared/log' '/srv/app/shared'\n" +
		"missing=0\n" +
		"[ -e '/srv/app/shared/.env' ] || { echo 'deploy:check: missing linked file:' '/srv/app/shared/.env' >&2; missing=1; }\n" +
		`[ "$missing" = 0 ] || { echo 'deploy:check: create the missing linked file(s) in shared/ before deploying' >&2; exit 1; }`
	if got != want {
		t.Errorf("ensureStructureCmd:\n got: %q\nwant: %q", got, want)
	}
}

// With no linked files there is no file check - just the mkdir tree.
func TestEnsureStructureCmd_NoLinkedFiles(t *testing.T) {
	l := paths.For("/srv/app")
	got := ensureStructureCmd(l, nil, []string{"log"})
	want := "set -e\nmkdir -p '/srv/app/releases' '/srv/app/shared' '/srv/app/shared/log'"
	if got != want {
		t.Errorf("ensureStructureCmd(no files):\n got: %q\nwant: %q", got, want)
	}
}

// Functionally: the check creates the dirs and fails when a linked file is missing from shared/, then passes once it's
// present.
func TestEnsureStructureCmd_VerifiesLinkedFiles(t *testing.T) {
	root := t.TempDir()
	l := paths.For(filepath.Join(root, "app"))
	script := ensureStructureCmd(l, []string{"config/database.yml"}, []string{"log"})

	// Missing linked file -> non-zero exit, and the dirs are still created.
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure for a missing linked file, output:\n%s", out)
	}
	if !strings.Contains(string(out), "missing linked file") {
		t.Errorf("missing-file message absent:\n%s", out)
	}
	if _, statErr := os.Stat(l.SharedPath); statErr != nil {
		t.Errorf("shared dir should have been created even on failure: %v", statErr)
	}

	// Provide the file -> the check passes.
	if err := os.WriteFile(filepath.Join(l.SharedPath, "config", "database.yml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("check should pass once the linked file exists: %v\n%s", err, out)
	}
}

func TestSymlinkSharedScript(t *testing.T) {
	l := paths.For("/srv/app")
	got := symlinkSharedScript(l, l.Release("123"), []string{".env"}, []string{"log"})
	want := "set -e\n" +
		"mkdir -p '/srv/app/shared/log' '/srv/app/releases/123'\n" +
		"rm -rf '/srv/app/releases/123/log'\n" +
		"ln -s '/srv/app/shared/log' '/srv/app/releases/123/log'\n" +
		"mkdir -p '/srv/app/shared' '/srv/app/releases/123'\n" +
		"ln -sf '/srv/app/shared/.env' '/srv/app/releases/123/.env'"
	if got != want {
		t.Errorf("symlinkSharedScript:\n got: %q\nwant: %q", got, want)
	}
}

// A "source:dest" entry links shared/source at release/dest (the destination is rewritten); a bare entry is unchanged.
func TestSymlinkSharedScript_RewritesDest(t *testing.T) {
	l := paths.For("/srv/app")
	got := symlinkSharedScript(l, l.Release("123"),
		[]string{"config/database.yml:config/new-database.yml"},
		[]string{"shared-log:log"})
	want := "set -e\n" +
		"mkdir -p '/srv/app/shared/shared-log' '/srv/app/releases/123'\n" +
		"rm -rf '/srv/app/releases/123/log'\n" +
		"ln -s '/srv/app/shared/shared-log' '/srv/app/releases/123/log'\n" +
		"mkdir -p '/srv/app/shared/config' '/srv/app/releases/123/config'\n" +
		"ln -sf '/srv/app/shared/config/database.yml' '/srv/app/releases/123/config/new-database.yml'"
	if got != want {
		t.Errorf("symlinkSharedScript(rewrite):\n got: %q\nwant: %q", got, want)
	}
}

// deploy:check verifies the shared-side source exists, regardless of the rewritten destination.
func TestEnsureStructureCmd_RewriteChecksSource(t *testing.T) {
	l := paths.For("/srv/app")
	got := ensureStructureCmd(l, []string{"config/database.yml:config/new-database.yml"}, nil)
	if !strings.Contains(got, "'/srv/app/shared/config/database.yml'") {
		t.Errorf("check should verify the shared source path, got:\n%s", got)
	}
	if strings.Contains(got, "new-database.yml") {
		t.Errorf("check must not reference the release-side destination, got:\n%s", got)
	}
}

func TestCurrentRevisionCmd(t *testing.T) {
	l := paths.For("/srv/app")
	if got, want := currentRevisionCmd(l), `cat '/srv/app/current/REVISION' 2>/dev/null || true`; got != want {
		t.Errorf("currentRevisionCmd = %q, want %q", got, want)
	}
}

func TestChangelogCmd(t *testing.T) {
	got := changelogCmd("/srv/app/repo", "aaaaaaaa", "bbbbbbbb")
	want := `git -C '/srv/app/repo' log --no-merges --max-count=100 --pretty=format:'%H|%an|%ae|%s' 'aaaaaaaa..bbbbbbbb'`
	if got != want {
		t.Errorf("changelogCmd:\n got: %q\nwant: %q", got, want)
	}
}

func TestIsCommitSHA(t *testing.T) {
	for sha, want := range map[string]bool{
		"0f4c1a7": true,
		"0f4c1a7d9e2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d": true,
		"":                false,
		"HEAD":            false,
		"0f4c1a":          false, // too short
		"0F4C1A7":         false, // git prints lowercase
		"deadbeef\nextra": false,
	} {
		if got := isCommitSHA(sha); got != want {
			t.Errorf("isCommitSHA(%q) = %v, want %v", sha, got, want)
		}
	}
}

func TestUnlockCmd(t *testing.T) {
	l := paths.For("/srv/app")
	if got, want := unlockCmd(l), `rm -f '/srv/app/.deploy.lock'`; got != want {
		t.Errorf("unlockCmd = %q, want %q", got, want)
	}
}

func TestCleanupCmd(t *testing.T) {
	l := paths.For("/srv/app")
	got := cleanupCmd(l.ReleasesPath, 3)
	for _, want := range []string{"cd '/srv/app/releases' || exit 0", "remove=$((count - 3))", `head -n "$remove"`} {
		if !strings.Contains(got, want) {
			t.Errorf("cleanupCmd missing %q in:\n%s", want, got)
		}
	}
}

func TestLockCmd(t *testing.T) {
	l := paths.For("/srv/app")
	got := lockCmd(l, "me @ now")
	for _, want := range []string{"mkdir -p '/srv/app'", "set -C; printf '%s' 'me @ now' > '/srv/app/.deploy.lock'", "stage is locked by:"} {
		if !strings.Contains(got, want) {
			t.Errorf("lockCmd missing %q in:\n%s", want, got)
		}
	}
}

func TestRollbackScript(t *testing.T) {
	l := paths.For("/srv/app")
	noClean := rollbackScript(l, false)
	if !strings.Contains(noClean, `mv -T "/srv/app/current.tmp" "/srv/app/current"`) {
		t.Errorf("rollback missing atomic swap:\n%s", noClean)
	}
	if strings.Contains(noClean, "removed rolled-back release") {
		t.Errorf("rollback(false) should not clean up:\n%s", noClean)
	}
	withClean := rollbackScript(l, true)
	if !strings.Contains(withClean, `rm -rf "/srv/app/releases/$curbase"`) || !strings.Contains(withClean, "removed rolled-back release") {
		t.Errorf("rollback(true) missing cleanup:\n%s", withClean)
	}
}

func TestReleasesScript(t *testing.T) {
	l := paths.For("/srv/app")
	got := releasesScript(l)
	for _, want := range []string{`for r in $(ls -1 "/srv/app/releases" | sort)`, `echo "$r (current)"`} {
		if !strings.Contains(got, want) {
			t.Errorf("releasesScript missing %q in:\n%s", want, got)
		}
	}
}
