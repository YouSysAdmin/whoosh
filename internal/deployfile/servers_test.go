package deployfile_test

import (
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

func TestHost_DeployEnabled(t *testing.T) {
	cases := []struct {
		name string
		s    ast.Host
		want bool
	}{
		{"nil defaults true", ast.Host{}, true},
		{"explicit true", ast.Host{Deploy: new(true)}, true},
		{"explicit false", ast.Host{Deploy: new(false)}, false},
	}
	for _, c := range cases {
		if got := c.s.DeployEnabled(); got != c.want {
			t.Errorf("%s: DeployEnabled() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTasksForStage(t *testing.T) {
	shared := `app:
  name: a
  repo: git@example.com:a.git
  deploy_to: /srv/a
tasks:
  bundle:
    desc: Install gems
    cmds: [echo bundle]
`
	stage := `tasks:
  restore-manifest:
    desc: Restore the asset manifest
    hidden: true
    cmds: [echo restore]
`
	path := writeProject(t, shared, "prod", stage)

	// Stage-merged discovery sees both base and stage-only tasks, with the full task so callers can read Desc and Hidden.
	merged, err := deployfile.TasksForStage(path, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if merged["bundle"] == nil || merged["bundle"].Desc != "Install gems" {
		t.Errorf("missing shared task: %v", merged["bundle"])
	}
	rm := merged["restore-manifest"]
	if rm == nil {
		t.Fatalf("missing stage-only task")
	}
	if rm.Desc != "Restore the asset manifest" || !rm.Hidden {
		t.Errorf("stage task fields wrong: desc=%q hidden=%v", rm.Desc, rm.Hidden)
	}

	// Unknown stage falls back to the shared file's tasks (best-effort).
	fallback, err := deployfile.TasksForStage(path, "does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fallback["bundle"]; !ok {
		t.Errorf("fallback should still list shared tasks: %v", fallback)
	}
	if _, ok := fallback["restore-manifest"]; ok {
		t.Errorf("fallback must not invent stage tasks: %v", fallback)
	}
}

func TestFilterDeployable(t *testing.T) {
	hosts := []ast.Host{
		{Address: "a"},                     // nil -> deployable
		{Address: "b", Deploy: new(false)}, // excluded
		{Address: "c", Deploy: new(true)},  // deployable
	}
	got := ast.FilterDeployable(hosts)
	if len(got) != 2 {
		t.Fatalf("want 2 deployable, got %d: %+v", len(got), got)
	}
	if got[0].Address != "a" || got[1].Address != "c" {
		t.Errorf("FilterDeployable = %+v, want hosts a,c", got)
	}
}

func TestFilterNonDeployable(t *testing.T) {
	hosts := []ast.Host{
		{Address: "a"},                     // nil -> deployable, excluded here
		{Address: "b", Deploy: new(false)}, // non-deployable
		{Address: "c", Deploy: new(true)},  // deployable, excluded here
	}
	got := ast.FilterNonDeployable(hosts)
	if len(got) != 1 || got[0].Address != "b" {
		t.Errorf("FilterNonDeployable = %+v, want host b only", got)
	}
}

func TestPluginSpec_ActiveForStage(t *testing.T) {
	cases := []struct {
		name  string
		spec  ast.PluginSpec
		stage string
		want  bool
	}{
		{"no filters", ast.PluginSpec{}, "staging", true},
		{"only match", ast.PluginSpec{Only: []string{"production", "uat"}}, "uat", true},
		{"only miss", ast.PluginSpec{Only: []string{"production", "uat"}}, "staging", false},
		{"except match", ast.PluginSpec{Except: []string{"staging"}}, "staging", false},
		{"except miss", ast.PluginSpec{Except: []string{"staging"}}, "production", true},
		{"except wins over only", ast.PluginSpec{Only: []string{"staging"}, Except: []string{"staging"}}, "staging", false},
	}
	for _, c := range cases {
		if got := c.spec.ActiveForStage(c.stage); got != c.want {
			t.Errorf("%s: ActiveForStage(%q) = %v, want %v", c.name, c.stage, got, c.want)
		}
	}
}

func TestTask_ActiveForStage(t *testing.T) {
	cases := []struct {
		name  string
		task  ast.Task
		stage string
		want  bool
	}{
		{"no filters", ast.Task{}, "staging", true},
		{"only match", ast.Task{Only: []string{"production", "uat"}}, "uat", true},
		{"only miss", ast.Task{Only: []string{"production", "uat"}}, "staging", false},
		{"except match", ast.Task{Except: []string{"staging"}}, "staging", false},
		{"except miss", ast.Task{Except: []string{"staging"}}, "production", true},
		{"except wins over only", ast.Task{Only: []string{"staging"}, Except: []string{"staging"}}, "staging", false},
	}
	for _, c := range cases {
		if got := c.task.ActiveForStage(c.stage); got != c.want {
			t.Errorf("%s: ActiveForStage(%q) = %v, want %v", c.name, c.stage, got, c.want)
		}
	}
}
