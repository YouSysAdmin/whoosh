package deploy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yousysadmin/whoosh/internal/deploy"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// initSourceRepo creates a git repo with one committed file on branch main.
func initSourceRepo(t *testing.T, file, content string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "initial")
	return dir
}

type fixture struct {
	cfg      *ast.DeployFile
	deployTo string
}

// projectConfig sets up a source repo, deploy_to, and a shared/.env, returning a config with no servers attached yet.
func projectConfig(t *testing.T, keep int) *ast.DeployFile {
	t.Helper()
	srcRepo := initSourceRepo(t, "app.txt", "v1")
	deployTo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deployTo, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployTo, "shared", ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &ast.DeployFile{
		App:         ast.App{Name: "myapp", Repo: srcRepo, Branch: "main", DeployTo: deployTo, KeepReleases: keep},
		LinkedFiles: []string{".env"},
		LinkedDirs:  []string{"log"},
		Stage:       "test",
	}
}

func newFixture(t *testing.T, keep int) *fixture {
	t.Helper()
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(srv.Close)

	cfg := projectConfig(t, keep)
	cfg.Hosts = []ast.Host{{
		Address: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Roles: []string{"app"},
	}}
	return &fixture{cfg: cfg, deployTo: cfg.App.DeployTo}
}

// newLocalFixture deploys in local execution mode: a single local server, no SSH.
func newLocalFixture(t *testing.T, keep int) *fixture {
	t.Helper()
	cfg := projectConfig(t, keep)
	cfg.Hosts = []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}}
	return &fixture{cfg: cfg, deployTo: cfg.App.DeployTo}
}

func (f *fixture) deployer(t *testing.T) (*deploy.Deployer, *bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	d, err := deploy.New(f.cfg, ex)
	if err != nil {
		t.Fatalf("deploy.New: %v", err)
	}
	return d, &buf, ex.Close
}

func (f *fixture) deploy(t *testing.T) {
	t.Helper()
	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Deploy(context.Background()); err != nil {
		t.Fatalf("deploy: %v\noutput:\n%s", err, buf.String())
	}
}

func (f *fixture) currentTarget(t *testing.T) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(f.deployTo, "current"))
	if err != nil {
		t.Fatalf("readlink current: %v", err)
	}
	return filepath.Base(target)
}

func (f *fixture) releaseNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.deployTo, "releases"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestDeploy_FullLifecycle(t *testing.T) {
	f := newFixture(t, 1)
	f.deploy(t)

	current := filepath.Join(f.deployTo, "current")
	target, err := os.Readlink(current)
	if err != nil {
		t.Fatalf("current is not a symlink: %v", err)
	}
	if filepath.Dir(target) != filepath.Join(f.deployTo, "releases") {
		t.Fatalf("current points outside releases/: %s", target)
	}
	if got, err := os.ReadFile(filepath.Join(current, "app.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("app.txt in release = %q, %v, want v1", got, err)
	}
	if lt, err := os.Readlink(filepath.Join(current, "log")); err != nil || lt != filepath.Join(f.deployTo, "shared", "log") {
		t.Fatalf("log symlink = %q, %v", lt, err)
	}
	if got, err := os.ReadFile(filepath.Join(current, ".env")); err != nil || string(got) != "SECRET=1" {
		t.Fatalf(".env via release = %q, %v, want SECRET=1", got, err)
	}

	// Revision tracking: REVISION (git SHA), REVISION_TIME (RFC3339), revisions.log.
	rev, err := os.ReadFile(filepath.Join(current, "REVISION"))
	if err != nil {
		t.Fatalf("REVISION: %v", err)
	}
	sha := strings.TrimSpace(string(rev))
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		t.Fatalf("REVISION = %q, want a 40-char git SHA", sha)
	}
	if rt, err := os.ReadFile(filepath.Join(current, "REVISION_TIME")); err != nil {
		t.Fatalf("REVISION_TIME: %v", err)
	} else if _, err := time.Parse(time.RFC3339, strings.TrimSpace(string(rt))); err != nil {
		t.Fatalf("REVISION_TIME = %q not RFC3339: %v", rt, err)
	}
	logb, err := os.ReadFile(filepath.Join(f.deployTo, "revisions.log"))
	if err != nil {
		t.Fatalf("revisions.log: %v", err)
	}
	wantLine := "Branch main (at " + sha + ") deployed as release " + f.currentTarget(t) + " by "
	if !strings.Contains(string(logb), wantLine) {
		t.Fatalf("revisions.log missing %q, got:\n%s", wantLine, logb)
	}

	// Second deploy with keep_releases=1 must rotate down to one release.
	time.Sleep(1100 * time.Millisecond)
	f.deploy(t)
	if names := f.releaseNames(t); len(names) != 1 {
		t.Fatalf("keep_releases=1 but %d releases remain: %v", len(names), names)
	}
	if f.currentTarget(t) != f.releaseNames(t)[0] {
		t.Fatalf("current does not point at the surviving release")
	}
}

// TestDeploy_CommitHashInContext verifies the deployed commit SHA is captured into the context and reaches a hook task
// as both $COMMIT_HASH (env) and {{.commit_hash}} (template), matching the REVISION file written on the host.
func TestDeploy_CommitHashInContext(t *testing.T) {
	f := newFixture(t, 5)
	f.cfg.Tasks = map[string]*ast.Task{
		"show-rev": {
			Roles: []string{"app"},
			Cmds:  []string{`echo "rev=$COMMIT_HASH tmpl={{.commit_hash}}"`},
		},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhasePublishing: {"show-rev"}}}

	var buf bytes.Buffer
	ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	d, err := deploy.New(f.cfg, ex)
	if err != nil {
		t.Fatalf("deploy.New: %v", err)
	}
	if err := d.Deploy(context.Background()); err != nil {
		ex.Close()
		t.Fatalf("deploy: %v\n%s", err, buf.String())
	}
	ex.Close() // flush captured output

	rev, err := os.ReadFile(filepath.Join(f.deployTo, "current", "REVISION"))
	if err != nil {
		t.Fatalf("REVISION: %v", err)
	}
	sha := strings.TrimSpace(string(rev))
	want := "rev=" + sha + " tmpl=" + sha
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("commit hash not in context: want %q in\n%s", want, buf.String())
	}
}

// TestDeploy_PreviousCommitHashInContext verifies the previously deployed SHA is read off the host at deploy start:
// empty on the first deploy, the first deploy's SHA on the second - as both env var and template key, and visible
// already to deploy:starting hooks.
func TestDeploy_PreviousCommitHashInContext(t *testing.T) {
	f := newFixture(t, 5)
	f.cfg.Tasks = map[string]*ast.Task{
		"show-prev": {
			Roles: []string{"app"},
			Dir:   f.deployTo, // deploy:starting runs before the release dir exists
			Cmds:  []string{`echo "prev=[$PREVIOUS_COMMIT_HASH] tmpl=[{{.previous_commit_hash}}]"`},
		},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhaseStarting: {"show-prev"}}}

	deployOnce := func() string {
		var buf bytes.Buffer
		ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
		d, err := deploy.New(f.cfg, ex)
		if err != nil {
			t.Fatalf("deploy.New: %v", err)
		}
		if err := d.Deploy(context.Background()); err != nil {
			ex.Close()
			t.Fatalf("deploy: %v\n%s", err, buf.String())
		}
		ex.Close()
		return buf.String()
	}

	if out := deployOnce(); !strings.Contains(out, "prev=[] tmpl=[]") {
		t.Fatalf("first deploy should see an empty previous_commit_hash, got:\n%s", out)
	}

	rev, err := os.ReadFile(filepath.Join(f.deployTo, "current", "REVISION"))
	if err != nil {
		t.Fatalf("REVISION: %v", err)
	}
	sha := strings.TrimSpace(string(rev))

	time.Sleep(1100 * time.Millisecond) // release ids are second-granular
	want := "prev=[" + sha + "] tmpl=[" + sha + "]"
	if out := deployOnce(); !strings.Contains(out, want) {
		t.Fatalf("second deploy: want %q in\n%s", want, out)
	}
}

// addCommit commits a file change to a test source repo (same identity as initSourceRepo).
func addCommit(t *testing.T, repoDir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestDeploy_ChangelogInContext verifies the commit range between the previous and the new revision is captured at
// deploy:updating and exposed as {{.changelog}} / $DEPLOY_CHANGELOG: empty on the first deploy, the new commits on
// the second, empty again when nothing changed.
func TestDeploy_ChangelogInContext(t *testing.T) {
	f := newFixture(t, 5)
	f.cfg.Tasks = map[string]*ast.Task{
		"show-log": {
			Roles: []string{"app"},
			Cmds:  []string{`echo "env=[$DEPLOY_CHANGELOG] tmpl=[{{.changelog}}]"`},
		},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhasePublished: {"show-log"}}}

	deployOnce := func() string {
		var buf bytes.Buffer
		ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
		d, err := deploy.New(f.cfg, ex)
		if err != nil {
			t.Fatalf("deploy.New: %v", err)
		}
		if err := d.Deploy(context.Background()); err != nil {
			ex.Close()
			t.Fatalf("deploy: %v\n%s", err, buf.String())
		}
		ex.Close()
		return buf.String()
	}

	if out := deployOnce(); !strings.Contains(out, "env=[] tmpl=[]") {
		t.Fatalf("first deploy should see an empty changelog, got:\n%s", out)
	}

	addCommit(t, f.cfg.App.Repo, "app.txt", "v2", "feat: second change")
	time.Sleep(1100 * time.Millisecond) // release ids are second-granular
	out := deployOnce()
	if !strings.Contains(out, "|test|test@example.com|feat: second change") {
		t.Fatalf("second deploy should carry the new commit in the changelog, got:\n%s", out)
	}

	time.Sleep(1100 * time.Millisecond)
	if out := deployOnce(); !strings.Contains(out, "env=[] tmpl=[]") {
		t.Fatalf("unchanged redeploy should see an empty changelog, got:\n%s", out)
	}
}

// TestDeploy_FailureHookNotifies verifies a deploy:failed hook runs when the deploy errors, with the phase and failure
// message exposed to the task.
func TestDeploy_FailureHookNotifies(t *testing.T) {
	f := newFixture(t, 5)
	f.cfg.App.Repo = "/nonexistent/repo.git" // EnsureMirror (git clone) will fail
	f.cfg.Tasks = map[string]*ast.Task{
		"on-fail": {Local: true, Cmds: []string{`echo "FAILHOOK phase=$DEPLOY_PHASE tmpl={{.phase}} err=$DEPLOY_ERROR"`}},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhaseFailed: {"on-fail"}}}

	var buf bytes.Buffer
	ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	d, err := deploy.New(f.cfg, ex)
	if err != nil {
		t.Fatalf("deploy.New: %v", err)
	}
	err = d.Deploy(context.Background())
	ex.Close()

	if err == nil {
		t.Fatalf("expected the deploy to fail with a bad repo, output:\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "FAILHOOK phase=deploy:failed tmpl=deploy:failed") {
		t.Fatalf("deploy:failed hook did not run with the phase context:\n%s", out)
	}
	// The failure message (which names the failed phase) reaches the hook.
	if !strings.Contains(out, "deploy:updating") {
		t.Fatalf("deploy:failed hook missing $DEPLOY_ERROR detail:\n%s", out)
	}
}

// TestDeploy_FailureFuncHookRuns verifies a plugin func-hook on deploy:failed fires even when no task hook is
// registered for the phase (a plugin failure-notifier must not depend on the Deployfile also wiring a task).
func TestDeploy_FailureFuncHookRuns(t *testing.T) {
	f := newFixture(t, 5)
	f.cfg.App.Repo = "/nonexistent/repo.git" // EnsureMirror (git clone) will fail

	var fired bool
	f.cfg.AddHookFuncAfter(ast.PhaseFailed, func(_ context.Context, out io.Writer) error {
		fired = true
		_, err := fmt.Fprintln(out, "FUNCHOOK notified")
		return err
	})

	var buf bytes.Buffer
	ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	d, err := deploy.New(f.cfg, ex)
	if err != nil {
		t.Fatalf("deploy.New: %v", err)
	}
	err = d.Deploy(context.Background())
	ex.Close()

	if err == nil {
		t.Fatalf("expected the deploy to fail with a bad repo, output:\n%s", buf.String())
	}
	if !fired {
		t.Fatalf("deploy:failed func-hook did not fire, output:\n%s", buf.String())
	}
}

// closedPort reserves and releases a localhost port so a dial to it is refused (fast), simulating an unreachable host.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// withDeadHost appends an unreachable secondary host (Host "localhost" so its host string differs from the live
// 127.0.0.1 server - the exclusion set is keyed by host string).
func (f *fixture) withDeadHost(t *testing.T, required bool) {
	t.Helper()
	s := ast.Host{
		Address: "localhost", Port: closedPort(t), User: "deploy",
		IdentityFile: f.cfg.Hosts[0].IdentityFile, Roles: []string{"app"},
	}
	if required {
		s.Required = new(true)
	}
	f.cfg.Hosts = append(f.cfg.Hosts, s)
}

func (f *fixture) runDeploy(t *testing.T) error {
	t.Helper()
	var buf bytes.Buffer
	ex := executor.New(f.cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	d, err := deploy.New(f.cfg, ex)
	if err != nil {
		t.Fatalf("deploy.New: %v", err)
	}
	err = d.Deploy(context.Background())
	ex.Close()
	if err != nil {
		t.Logf("deploy output:\n%s", buf.String())
	}
	return err
}

func TestDeploy_SkipUnreachableHost(t *testing.T) {
	f := newFixture(t, 5) // servers[0] = live sshtest host (the lock primary)
	f.withDeadHost(t, false)
	f.cfg.OnUnreachable = ast.OnUnreachableSkip

	err := f.runDeploy(t)
	var skipped *errors.SkippedHostsError
	if !errors.As(err, &skipped) {
		t.Fatalf("want SkippedHostsError, got %v", err)
	}
	if len(skipped.Hosts) != 1 || skipped.Hosts[0] != "localhost" {
		t.Errorf("skipped hosts = %v, want [localhost]", skipped.Hosts)
	}
	// The deploy still completed on the surviving host.
	if _, err := os.Readlink(filepath.Join(f.deployTo, "current")); err != nil {
		t.Fatalf("current symlink missing on the surviving host: %v", err)
	}
}

func TestDeploy_RequiredUnreachableAborts(t *testing.T) {
	f := newFixture(t, 5)
	f.withDeadHost(t, true) // marked required
	f.cfg.OnUnreachable = ast.OnUnreachableSkip

	err := f.runDeploy(t)
	if err == nil {
		t.Fatal("expected the deploy to abort on a required unreachable host")
	}
	var se *errors.SkippedHostsError
	if errors.As(err, &se) {
		t.Fatalf("required host must abort, not skip: %v", err)
	}
}

func TestDeploy_AbortOnUnreachableByDefault(t *testing.T) {
	f := newFixture(t, 5)
	f.withDeadHost(t, false)
	// no OnUnreachable set -> default "abort"

	err := f.runDeploy(t)
	if err == nil {
		t.Fatal("expected the deploy to abort (default policy) on an unreachable host")
	}
	var se *errors.SkippedHostsError
	if errors.As(err, &se) {
		t.Fatalf("default policy must abort, not skip: %v", err)
	}
}

func TestDeploy_LocalMode(t *testing.T) {
	// Full lifecycle with no SSH: a single local server.
	f := newLocalFixture(t, 5)
	f.deploy(t)

	current := filepath.Join(f.deployTo, "current")
	if got, err := os.ReadFile(filepath.Join(current, "app.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("local deploy: app.txt = %q, %v, want v1", got, err)
	}
	if lt, err := os.Readlink(filepath.Join(current, "log")); err != nil || lt != filepath.Join(f.deployTo, "shared", "log") {
		t.Fatalf("local deploy: log symlink = %q, %v", lt, err)
	}
	if got, err := os.ReadFile(filepath.Join(current, ".env")); err != nil || string(got) != "SECRET=1" {
		t.Fatalf("local deploy: .env = %q, %v; want SECRET=1", got, err)
	}

	// Rollback also works locally.
	time.Sleep(1100 * time.Millisecond)
	f.deploy(t)
	names := f.releaseNames(t)
	if len(names) != 2 {
		t.Fatalf("want 2 releases, got %v", names)
	}
	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Rollback(context.Background(), false); err != nil {
		t.Fatalf("local rollback: %v\n%s", err, buf.String())
	}
	if f.currentTarget(t) != names[0] {
		t.Fatalf("after local rollback current = %s, want %s", f.currentTarget(t), names[0])
	}
}

func TestDeploy_Rollback(t *testing.T) {
	f := newFixture(t, 5)
	f.deploy(t)
	time.Sleep(1100 * time.Millisecond)
	f.deploy(t)

	names := f.releaseNames(t)
	if len(names) != 2 {
		t.Fatalf("want 2 releases, got %v", names)
	}
	older, newer := names[0], names[1]
	if f.currentTarget(t) != newer {
		t.Fatalf("current = %s, want newest %s", f.currentTarget(t), newer)
	}

	// Roll back to the older release.
	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Rollback(context.Background(), false); err != nil {
		t.Fatalf("rollback: %v\n%s", err, buf.String())
	}
	if f.currentTarget(t) != older {
		t.Fatalf("after rollback current = %s, want %s", f.currentTarget(t), older)
	}
	// Without --cleanup both releases remain.
	if names := f.releaseNames(t); len(names) != 2 {
		t.Fatalf("rollback should not remove releases, got %v", names)
	}

	// Rolling back from the oldest release must fail.
	d2, _, closeFn2 := f.deployer(t)
	defer closeFn2()
	if err := d2.Rollback(context.Background(), false); err == nil {
		t.Fatal("expected error rolling back past the oldest release")
	}
}

func TestDeploy_MarkerPhaseHooks(t *testing.T) {
	f := newFixture(t, 5)
	// One task hooked to the marker phases; it appends the phase it ran for to a log. dir is deploy_to (exists from the
	// start) so the init/started hooks - which fire before the release dir exists - have a valid cwd.
	logPath := filepath.Join(f.deployTo, "PHASES")
	f.cfg.Tasks = map[string]*ast.Task{
		"mark": {Roles: []string{"app"}, Dir: f.deployTo, Cmds: []string{"echo {{.phase}} >> " + logPath}},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{
		ast.PhaseInit:      {"mark"},
		ast.PhaseStarted:   {"mark"},
		ast.PhaseUpdated:   {"mark"},
		ast.PhasePublished: {"mark"},
	}}

	f.deploy(t)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("marker-phase hooks did not run: %v", err)
	}
	got := strings.Join(strings.Fields(string(data)), ",")
	want := strings.Join([]string{ast.PhaseInit, ast.PhaseStarted, ast.PhaseUpdated, ast.PhasePublished}, ",")
	if got != want {
		t.Fatalf("marker phases ran as %q, want %q", got, want)
	}
}

func TestDeploy_RollbackHooks(t *testing.T) {
	f := newFixture(t, 5)
	// An after-deploy:rollback hook stamps the phase + which release current points at, into that release.
	// It must run AFTER the swap, so the stamp lands in the restored (older) release.
	f.cfg.Tasks = map[string]*ast.Task{
		"stamp-rollback": {Roles: []string{"app"}, Cmds: []string{"echo {{.phase}} > {{.current_path}}/ROLLED_BACK"}},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhaseRollback: {"stamp-rollback"}}}

	f.deploy(t)
	time.Sleep(1100 * time.Millisecond)
	f.deploy(t)

	names := f.releaseNames(t)
	older := names[0]

	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Rollback(context.Background(), false); err != nil {
		t.Fatalf("rollback: %v\n%s", err, buf.String())
	}
	if f.currentTarget(t) != older {
		t.Fatalf("after rollback current = %s, want %s", f.currentTarget(t), older)
	}

	// The hook ran with $DEPLOY_PHASE set, against the restored release.
	stamp, err := os.ReadFile(filepath.Join(f.deployTo, "releases", older, "ROLLED_BACK"))
	if err != nil {
		t.Fatalf("after deploy:rollback hook did not run against the restored release: %v", err)
	}
	if got := strings.TrimSpace(string(stamp)); got != ast.PhaseRollback {
		t.Fatalf("hook phase = %q, want %q", got, ast.PhaseRollback)
	}
}

func TestDeploy_RollbackReplace(t *testing.T) {
	f := newFixture(t, 5)
	f.deploy(t)
	time.Sleep(1100 * time.Millisecond)
	f.deploy(t)
	newest := f.currentTarget(t)

	// A task that takes over deploy:rollback: it runs instead of the symlink swap.
	marker := filepath.Join(f.deployTo, "REPLACED_ROLLBACK")
	f.cfg.Tasks = map[string]*ast.Task{
		"myrollback": {Local: true, Replace: ast.PhaseRollback, Cmds: []string{"echo yes > " + marker}},
	}

	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Rollback(context.Background(), false); err != nil {
		t.Fatalf("rollback: %v\n%s", err, buf.String())
	}

	// The replacement task ran...
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("replacement task did not run: %v", err)
	}
	// ...and the built-in current-symlink swap did NOT (current is unchanged).
	if got := f.currentTarget(t); got != newest {
		t.Errorf("current = %s, want it unchanged at %s (built-in rollback should be replaced)", got, newest)
	}
}

func TestDeploy_New_RejectsUnreplaceablePhase(t *testing.T) {
	cfg := projectConfig(t, 5)
	cfg.Tasks = map[string]*ast.Task{
		"x": {Local: true, Replace: ast.PhasePublishing, Cmds: []string{"true"}},
	}
	ex := executor.New(cfg, executor.Options{Out: &bytes.Buffer{}})
	defer ex.Close()
	if _, err := deploy.New(cfg, ex); err == nil {
		t.Fatal("expected an error: deploy:publishing is not replaceable")
	}
}

func TestDeploy_New_RejectsDuplicateReplace(t *testing.T) {
	cfg := projectConfig(t, 5)
	cfg.Tasks = map[string]*ast.Task{
		"a": {Local: true, Replace: ast.PhaseRollback, Cmds: []string{"true"}},
		"b": {Local: true, Replace: ast.PhaseRollback, Cmds: []string{"true"}},
	}
	ex := executor.New(cfg, executor.Options{Out: &bytes.Buffer{}})
	defer ex.Close()
	if _, err := deploy.New(cfg, ex); err == nil {
		t.Fatal("expected an error: two tasks replace deploy:rollback")
	}
}

func TestDeploy_CustomPhase(t *testing.T) {
	f := newFixture(t, 5)
	// A custom phase "deploy:migrate" right after the release goes live, running the "migrate" task.
	// "pre" runs as a before-hook on the custom phase, proving a custom phase is itself a hook anchor.
	// Both record the phase they ran in.
	logPath := filepath.Join(f.deployTo, "PHASES")
	f.cfg.Tasks = map[string]*ast.Task{
		"migrate": {Roles: []string{"app"}, Dir: f.deployTo, Cmds: []string{"echo migrate:{{.phase}} >> " + logPath}},
		"pre":     {Roles: []string{"app"}, Dir: f.deployTo, Cmds: []string{"echo pre:{{.phase}} >> " + logPath}},
	}
	f.cfg.CustomPhases = []ast.CustomPhase{
		{Name: "deploy:migrate", After: ast.PhasePublished, Task: "migrate"},
	}
	f.cfg.Hooks = ast.Hooks{Before: map[string][]string{"deploy:migrate": {"pre"}}}

	f.deploy(t)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("custom phase did not run: %v", err)
	}
	got := strings.Join(strings.Fields(string(data)), ",")
	// The before-hook fires first, then the phase's task; both see {{.phase}} = the custom phase name.
	want := "pre:deploy:migrate,migrate:deploy:migrate"
	if got != want {
		t.Fatalf("custom phase ran as %q, want %q", got, want)
	}
}

func TestDeploy_New_RejectsBadCustomPhaseAnchor(t *testing.T) {
	cfg := projectConfig(t, 5)
	cfg.CustomPhases = []ast.CustomPhase{{Name: "deploy:x", After: "deploy:nope"}}
	ex := executor.New(cfg, executor.Options{Out: &bytes.Buffer{}})
	defer ex.Close()
	if _, err := deploy.New(cfg, ex); err == nil {
		t.Fatal("expected an error: anchor is not a built-in phase")
	}
}

func TestDeploy_New_RejectsCustomPhaseBuiltinName(t *testing.T) {
	cfg := projectConfig(t, 5)
	cfg.CustomPhases = []ast.CustomPhase{{Name: ast.PhasePublishing, After: ast.PhaseCheck}}
	ex := executor.New(cfg, executor.Options{Out: &bytes.Buffer{}})
	defer ex.Close()
	if _, err := deploy.New(cfg, ex); err == nil {
		t.Fatal("expected an error: name collides with a built-in phase")
	}
}

func TestDeploy_New_RejectsCustomPhaseMissingTask(t *testing.T) {
	cfg := projectConfig(t, 5)
	cfg.CustomPhases = []ast.CustomPhase{{Name: "deploy:x", After: ast.PhaseCheck, Task: "ghost"}}
	ex := executor.New(cfg, executor.Options{Out: &bytes.Buffer{}})
	defer ex.Close()
	if _, err := deploy.New(cfg, ex); err == nil {
		t.Fatal("expected an error: custom phase task not found")
	}
}

func TestDeploy_Releases(t *testing.T) {
	f := newFixture(t, 5)
	f.deploy(t)

	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	if err := d.Releases(context.Background()); err != nil {
		t.Fatalf("releases: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(current)") {
		t.Fatalf("releases output should mark the current release, got:\n%s", out)
	}
}

func TestDeploy_HooksRunWithReleaseContext(t *testing.T) {
	f := newFixture(t, 5)
	// A hook that writes the release timestamp into the release it ran against.
	f.cfg.Tasks = map[string]*ast.Task{
		"stamp": {Roles: []string{"app"}, Cmds: []string{"echo {{.release_timestamp}} > {{.release_path}}/STAMP"}},
	}
	f.cfg.Hooks = ast.Hooks{After: map[string][]string{ast.PhasePublishing: {"stamp"}}}

	f.deploy(t)

	stamp, err := os.ReadFile(filepath.Join(f.deployTo, "current", "STAMP"))
	if err != nil {
		t.Fatalf("hook did not create STAMP: %v", err)
	}
	if got := strings.TrimSpace(string(stamp)); got != f.currentTarget(t) {
		t.Fatalf("STAMP = %q, want release timestamp %q", got, f.currentTarget(t))
	}
}

func TestDeploy_LockContention(t *testing.T) {
	f := newFixture(t, 5)
	// Pre-create the lock as if another deploy holds it.
	lock := filepath.Join(f.deployTo, ".deploy.lock")
	if err := os.MkdirAll(f.deployTo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("someone @ now"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, buf, closeFn := f.deployer(t)
	defer closeFn()
	err := d.Deploy(context.Background())
	if err == nil {
		t.Fatalf("expected deploy to fail on held lock\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "lock") && !strings.Contains(buf.String(), "locked") {
		t.Fatalf("expected a lock error, got: %v\n%s", err, buf.String())
	}
	// The contended lock must be left intact (not stolen).
	if _, statErr := os.Stat(lock); statErr != nil {
		t.Fatalf("held lock was removed: %v", statErr)
	}
}

// TestDeploy_PhaseFuncHookRuns verifies a plugin-registered phase func hook runs during the deploy lifecycle and
// writes to the deploy's console (ex.Out()).
func TestDeploy_PhaseFuncHookRuns(t *testing.T) {
	f := newFixture(t, 1)
	f.cfg.AddHookFuncAfter(ast.PhaseStarting, func(_ context.Context, out io.Writer) error {
		fmt.Fprintln(out, "FUNC-HOOK-RAN")
		return nil
	})

	d, buf, closeFn := f.deployer(t)
	err := d.Deploy(context.Background())
	closeFn() // flush the captured output
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "FUNC-HOOK-RAN") {
		t.Errorf("phase func hook did not run / write to console:\n%s", buf.String())
	}
}
