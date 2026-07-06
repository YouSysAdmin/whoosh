package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
)

// notifyTaskFailure fires the after deploy:failed hooks for a failing top-level task command, like a failed deploy
// does, unless the task opts out with notify_failure: false.
func TestNotifyTaskFailure(t *testing.T) {
	newCfg := func(notify *bool) *ast.DeployFile {
		return &ast.DeployFile{
			App:   ast.App{Name: "myapp", DeployTo: t.TempDir()},
			Stage: "test",
			Tasks: map[string]*ast.Task{
				"boom":        {Local: true, NotifyFailure: notify, Cmds: []string{"exit 1"}},
				"notify-fail": {Local: true, Cmds: []string{"echo FAIL_HOOK_RAN err=$DEPLOY_ERROR"}},
			},
			Hooks: ast.Hooks{After: map[string][]string{ast.PhaseFailed: {"notify-fail"}}},
		}
	}

	run := func(cfg *ast.DeployFile) (string, error) {
		var buf bytes.Buffer
		ex := executor.New(cfg, executor.Options{Out: &buf})
		defer ex.Close()
		err := ex.RunTask(context.Background(), "boom")
		if err != nil {
			notifyTaskFailure(cfg, ex, "boom", err)
		}
		return buf.String(), err
	}

	t.Run("default fires the failed hooks", func(t *testing.T) {
		out, err := run(newCfg(nil))
		if err == nil {
			t.Fatal("boom should fail")
		}
		if !strings.Contains(out, "FAIL_HOOK_RAN") {
			t.Errorf("deploy:failed hook did not run, output:\n%s", out)
		}
		// The failure message reaches the hook task as $DEPLOY_ERROR.
		if !strings.Contains(out, "err=") || strings.Contains(out, "err=\n") {
			t.Errorf("hook task did not see $DEPLOY_ERROR, output:\n%s", out)
		}
	})

	t.Run("notify_failure false opts out", func(t *testing.T) {
		off := false
		out, err := run(newCfg(&off))
		if err == nil {
			t.Fatal("boom should fail")
		}
		if strings.Contains(out, "FAIL_HOOK_RAN") {
			t.Errorf("deploy:failed hook ran despite notify_failure: false, output:\n%s", out)
		}
	})

	t.Run("no failed hooks registered is a no-op", func(t *testing.T) {
		cfg := newCfg(nil)
		cfg.Hooks = ast.Hooks{}
		if _, err := run(cfg); err == nil {
			t.Fatal("boom should fail")
		}
	})
}
