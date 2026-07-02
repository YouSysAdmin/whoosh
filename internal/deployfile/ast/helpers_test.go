package ast

import "testing"

func TestDeployFile_AddTask_LazyInit(t *testing.T) {
	var c DeployFile // nil Tasks map
	c.AddTask("a", &Task{Desc: "x"})
	if got := c.Tasks["a"]; got == nil || got.Desc != "x" {
		t.Fatalf("AddTask did not register the task: %+v", c.Tasks)
	}
}

func TestDeployFile_AddHooks_LazyInitAndAppend(t *testing.T) {
	var c DeployFile // nil Hooks maps
	c.AddHookBefore("deploy:updating", "a", "b")
	c.AddHookAfter("deploy:published", "c")
	c.AddHookAfter("deploy:published", "d")

	if got := c.Hooks.Before["deploy:updating"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("AddHookBefore = %v, want [a b]", got)
	}
	if got := c.Hooks.After["deploy:published"]; len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("AddHookAfter should append: got %v, want [c d]", got)
	}
}

func TestDeployFile_AddPhase(t *testing.T) {
	var c DeployFile
	c.AddPhase(CustomPhase{Name: "deploy:migrate", After: "deploy:published", Task: "run"})
	if len(c.CustomPhases) != 1 || c.CustomPhases[0].Name != "deploy:migrate" {
		t.Fatalf("AddPhase = %+v", c.CustomPhases)
	}
}

func TestPluginSpec_IsEnabled(t *testing.T) {
	if !(PluginSpec{}).IsEnabled() {
		t.Error("omitted Enabled should be enabled")
	}
	if !(PluginSpec{Enabled: new(true)}).IsEnabled() {
		t.Error("Enabled=true should be enabled")
	}
	if (PluginSpec{Enabled: new(false)}).IsEnabled() {
		t.Error("Enabled=false should be disabled")
	}
}

// forward_agent is tri-state: a stage that says nothing inherits the base, and an explicit false disables forwarding
// the base enabled (previously impossible with a plain bool merge).
func TestMerge_SSHForwardAgentTriState(t *testing.T) {
	base := &DeployFile{SSH: SSH{ForwardAgent: new(true)}}
	if out := Merge(base, &DeployFile{}); out.SSH.ForwardAgent == nil || !*out.SSH.ForwardAgent {
		t.Errorf("unset override should keep base forward_agent=true, got %v", out.SSH.ForwardAgent)
	}
	if out := Merge(base, &DeployFile{SSH: SSH{ForwardAgent: new(false)}}); out.SSH.ForwardAgent == nil || *out.SSH.ForwardAgent {
		t.Errorf("explicit false override should disable forward_agent, got %v", out.SSH.ForwardAgent)
	}
}

func TestMerge_CustomPhasesConcatenate(t *testing.T) {
	base := &DeployFile{CustomPhases: []CustomPhase{{Name: "deploy:a", After: "deploy:check"}}}
	ov := &DeployFile{CustomPhases: []CustomPhase{{Name: "deploy:b", Before: "deploy:finishing"}}}
	out := Merge(base, ov)
	if len(out.CustomPhases) != 2 || out.CustomPhases[0].Name != "deploy:a" || out.CustomPhases[1].Name != "deploy:b" {
		t.Fatalf("merged custom phases = %+v, want [deploy:a deploy:b]", out.CustomPhases)
	}
	// Merge must not alias the inputs' backing arrays.
	base.CustomPhases[0].Name = "mutated"
	if out.CustomPhases[0].Name != "deploy:a" {
		t.Fatal("merged custom phases alias the base slice")
	}
}

func TestParseLinkedPath(t *testing.T) {
	cases := []struct{ in, wantSrc, wantDst string }{
		{"config/database.yml", "config/database.yml", "config/database.yml"},           // no rewrite: dest == source
		{"config/database.yml:config/new.yml", "config/database.yml", "config/new.yml"}, // rewrite
		{".env:shared.env", ".env", "shared.env"},                                       // file rewrite
		{"a:b:c", "a", "b:c"}, // split on first colon only
	}
	for _, c := range cases {
		src, dst := ParseLinkedPath(c.in)
		if src != c.wantSrc || dst != c.wantDst {
			t.Errorf("ParseLinkedPath(%q) = (%q, %q), want (%q, %q)", c.in, src, dst, c.wantSrc, c.wantDst)
		}
	}
}

func TestValidate_RejectsMalformedLinkedPath(t *testing.T) {
	base := DeployFile{App: App{Name: "a", DeployTo: "/srv/a"}, Version: "1"}
	for _, bad := range []string{":dest", "source:", ""} {
		c := base
		c.LinkedFiles = []string{bad}
		if err := c.Validate(); err == nil {
			t.Errorf("linked_files %q should be rejected", bad)
		}
	}
	// A bare path and a well-formed rewrite both validate.
	c := base
	c.LinkedFiles = []string{"config/database.yml", "config/database.yml:config/new.yml"}
	if err := c.Validate(); err != nil {
		t.Errorf("valid linked_files rejected: %v", err)
	}
}

func TestLog_RawOutput(t *testing.T) {
	if !(Log{}).RawOutput() {
		t.Fatal("unset raw_remote_log should default to raw (true)")
	}
	if !(Log{RawRemoteLog: new(true)}).RawOutput() {
		t.Fatal("raw_remote_log: true should be raw")
	}
	if (Log{RawRemoteLog: new(false)}).RawOutput() {
		t.Fatal("raw_remote_log: false should route through the logger")
	}
}

// When raw_remote_log is unset, the default follows the log format: text streams raw, json routes through slog (so a
// --log-format json / log.format: json run stays one valid JSON stream). An explicit raw_remote_log still wins.
func TestLog_RawOutput_FormatDefault(t *testing.T) {
	if (Log{Format: "json"}).RawOutput() {
		t.Fatal("json format should default to routing output through the logger (not raw)")
	}
	if (Log{Format: "JSON"}).RawOutput() {
		t.Fatal("json format check should be case-insensitive")
	}
	if !(Log{Format: "text"}).RawOutput() {
		t.Fatal("text format should default to raw")
	}
	// An explicit raw_remote_log: true wins even under json format (operator opted into raw).
	if !(Log{Format: "json", RawRemoteLog: new(true)}).RawOutput() {
		t.Fatal("explicit raw_remote_log: true should stay raw even for json format")
	}
}

func TestMerge_RawRemoteLogOverrides(t *testing.T) {
	// A stage explicitly opting out of raw streaming overrides the base default.
	out := Merge(&DeployFile{}, &DeployFile{Log: Log{RawRemoteLog: new(false)}})
	if out.Log.RawOutput() {
		t.Fatal("stage raw_remote_log: false did not override the base")
	}
	// An unset stage value leaves the base setting intact.
	out = Merge(&DeployFile{Log: Log{RawRemoteLog: new(false)}}, &DeployFile{})
	if out.Log.RawOutput() {
		t.Fatal("unset stage value clobbered the base raw_remote_log")
	}
}
