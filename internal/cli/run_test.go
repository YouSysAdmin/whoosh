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
		EnvFileValues: map[string]string{"FROM_DOTENV": "dotenv-value", "PLAIN": "overridden"},
		Imports:       map[string]map[string]string{"ssm": {"db-url": "postgres://h/db"}},
	}
	envs, err := renderRunEnvs(cfg, "/srv/app/current", false)
	if err != nil {
		t.Fatalf("renderRunEnvs: %v", err)
	}
	if envs["PLAIN"] != "value" {
		t.Errorf("PLAIN = %q, want value (global envs override env_files)", envs["PLAIN"])
	}
	if envs["TMPL"] != "fallback-prod" {
		t.Errorf("TMPL = %q, want fallback-prod (rendered, not the literal template)", envs["TMPL"])
	}
	// Task-env parity: env_files are the base layer and plugin imports are exported as $<NS>_<KEY>.
	if envs["FROM_DOTENV"] != "dotenv-value" {
		t.Errorf("FROM_DOTENV = %q, want dotenv-value (env_files base layer)", envs["FROM_DOTENV"])
	}
	if envs["SSM_DB_URL"] != "postgres://h/db" {
		t.Errorf("SSM_DB_URL = %q, want the plugin import value", envs["SSM_DB_URL"])
	}
}
