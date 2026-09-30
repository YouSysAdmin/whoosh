package cli

import (
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
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

	if got := envs["PLAIN"]; got != (executor.EnvValue{Value: "value", Expand: true}) {
		t.Errorf("PLAIN = %+v, want expandable value (global envs override env_files)", got)
	}

	if got := envs["TMPL"].Value; got != "fallback-prod" {
		t.Errorf("TMPL = %q, want fallback-prod (rendered, not the literal template)", got)
	}

	// Task-env parity: env_files are the base layer and plugin imports are exported as $<NS>_<KEY>, both literal.
	if got := envs["FROM_DOTENV"]; got != (executor.EnvValue{Value: "dotenv-value"}) {
		t.Errorf("FROM_DOTENV = %+v, want literal dotenv-value (env_files base layer)", got)
	}

	if got := envs["SSM_DB_URL"]; got != (executor.EnvValue{Value: "postgres://h/db"}) {
		t.Errorf("SSM_DB_URL = %+v, want the literal plugin import value", got)
	}
}
