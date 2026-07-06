package cli

import (
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// Ad-hoc run must export the global envs rendered, not the literal templates.
func TestRenderRunEnvs(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage: "prod",
		Envs: map[string]string{
			"PLAIN": "value",
			"TMPL":  `{{ env "WHOOSH_TEST_RUN_ENV" | default "fallback" }}-{{ .stage }}`,
		},
		EnvFileValues: map[string]string{},
	}
	envs, err := renderRunEnvs(cfg, "/srv/app/current", false)
	if err != nil {
		t.Fatalf("renderRunEnvs: %v", err)
	}
	if envs["PLAIN"] != "value" {
		t.Errorf("PLAIN = %q, want value", envs["PLAIN"])
	}
	if envs["TMPL"] != "fallback-prod" {
		t.Errorf("TMPL = %q, want fallback-prod (rendered, not the literal template)", envs["TMPL"])
	}

	// No envs -> nothing to render.
	cfg.Envs = nil
	if envs, err := renderRunEnvs(cfg, "/srv/app/current", false); err != nil || envs != nil {
		t.Errorf("empty envs: got %v, %v", envs, err)
	}
}
