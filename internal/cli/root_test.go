package cli

import (
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

func TestDetectStage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"simple", []string{"production", "deploy"}, "production", true},
		{"stage flags after action", []string{"staging", "deploy", "--verbose"}, "staging", true},
		{"root flag =value before stage", []string{"--log-level=debug", "prod", "deploy"}, "prod", true},
		{"root flag space value before stage", []string{"--log-level", "debug", "prod", "deploy"}, "prod", true},
		{"multiple valued root flags", []string{"--log-format", "json", "--log-output", "f.log", "prod", "deploy"}, "prod", true},
		{"bool root flag does not eat stage", []string{"--log-color", "prod", "deploy"}, "prod", true},
		{"reserved init", []string{"init"}, "", false},
		{"reserved version", []string{"version"}, "", false},
		{"only flags", []string{"--help"}, "", false},
		{"empty", nil, "", false},
		// Cobra completion driver: detect the stage from the args being completed, so `whoosh <stage> <TAB>` registers the
		// stage and offers its tasks.
		{"complete stage", []string{"__complete", "uat", ""}, "uat", true},
		{"complete nodesc stage", []string{"__completeNoDesc", "prod", "dep"}, "prod", true},
		{"complete first arg (no stage yet)", []string{"__complete", ""}, "", false},
		{"complete a reserved command", []string{"__complete", "version"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := detectStage(tc.args)
			if got != tc.want || ok != tc.ok {
				t.Errorf("detectStage(%v) = (%q, %v), want (%q, %v)", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRenderVars(t *testing.T) {
	t.Setenv("WHOOSH_TEST_PROC_VAR", "v1.2.3")
	cfg := &ast.DeployFile{
		App:           ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage:         "uat",
		EnvFileValues: map[string]string{"WHOOSH_TEST_FILE_VAR": "hook-url"},
		Vars: map[string]any{
			"app_version": `{{ env "WHOOSH_TEST_PROC_VAR" }}`, // process env
			"webhook":     `{{ env "WHOOSH_TEST_FILE_VAR" }}`, // env_files fallback
			"stage_name":  "{{ .stage }}",                     // static load-time key
			"nested":      map[string]any{"list": []any{"{{ .app_name }}"}},
			"count":       3, // non-string preserved
		},
	}
	if err := renderVars(cfg); err != nil {
		t.Fatalf("renderVars: %v", err)
	}
	if cfg.Vars["app_version"] != "v1.2.3" {
		t.Errorf("app_version = %v, want v1.2.3", cfg.Vars["app_version"])
	}
	if cfg.Vars["webhook"] != "hook-url" {
		t.Errorf("webhook (env_files fallback) = %v, want hook-url", cfg.Vars["webhook"])
	}
	if cfg.Vars["stage_name"] != "uat" {
		t.Errorf("stage_name = %v, want uat", cfg.Vars["stage_name"])
	}
	nested, _ := cfg.Vars["nested"].(map[string]any)
	list, _ := nested["list"].([]any)
	if len(list) != 1 || list[0] != "myapp" {
		t.Errorf("nested list not templated: %v", cfg.Vars["nested"])
	}
	if cfg.Vars["count"] != 3 {
		t.Errorf("int var changed: %v", cfg.Vars["count"])
	}
}

func TestRenderVars_SingleQuoteIsParseError(t *testing.T) {
	// Go template string args need double quotes; 'X' is a (single-char) rune literal.
	cfg := &ast.DeployFile{Vars: map[string]any{"app_version": `{{ env 'APP_VERSION' }}`}}
	err := renderVars(cfg)
	if err == nil {
		t.Fatal("single-quoted template arg should be a parse error")
	}
	if !strings.Contains(err.Error(), "app_version") {
		t.Errorf("error should name the var, got: %v", err)
	}
}

func TestRenderVars_VarInVarIsError(t *testing.T) {
	// Vars render against the static context only - one var cannot reference another.
	cfg := &ast.DeployFile{Vars: map[string]any{"a": "x", "b": "{{ .a }}"}}
	if err := renderVars(cfg); err == nil {
		t.Fatal("a var referencing another var should strict-fail")
	}
}

func TestRenderPluginParams(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage: "uat",
		Vars:  map[string]any{"bastion": "10.4.20.204", "deploy_user": "deployer"},
		Plugins: []ast.PluginSpec{{
			Name: "aws:ec2:inventory",
			Params: map[string]any{
				"credentials_from_host": map[string]any{
					"host": "{{ .bastion }}",     // from a stage var
					"user": "{{ .deploy_user }}", // from a stage var
				},
				"tags":          map[string]any{"Application": []any{"{{ .app_name }}"}}, // static config
				"use_public_ip": false,                                                   // non-string preserved
			},
		}},
	}
	if err := renderPluginParams(cfg); err != nil {
		t.Fatalf("renderPluginParams: %v", err)
	}

	p := cfg.Plugins[0].Params
	cfh, _ := p["credentials_from_host"].(map[string]any)
	if cfh == nil || cfh["host"] != "10.4.20.204" || cfh["user"] != "deployer" {
		t.Errorf("credentials_from_host not templated: %v", p["credentials_from_host"])
	}
	tags, _ := p["tags"].(map[string]any)
	app, _ := tags["Application"].([]any)
	if len(app) != 1 || app[0] != "myapp" {
		t.Errorf("tag list from {{.app_name}} not templated: %v", p["tags"])
	}
	if p["use_public_ip"] != false {
		t.Errorf("bool param changed: %v", p["use_public_ip"])
	}

	// An undefined var is a hard error (typo guard).
	bad := &ast.DeployFile{Plugins: []ast.PluginSpec{{Name: "x", Params: map[string]any{"h": "{{ .nope }}"}}}}
	if err := renderPluginParams(bad); err == nil {
		t.Error("expected error for an undefined var in plugins params")
	}
}

func TestSelectPluginsForStage(t *testing.T) {
	cfg := &ast.DeployFile{
		Stage: "staging",
		Plugins: []ast.PluginSpec{
			{Name: "aws", Except: []string{"staging"}},
			{Name: "other"},
		},
	}
	selectPluginsForStage(cfg)
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].Name != "other" {
		t.Errorf("active plugins = %+v, want [other]", cfg.Plugins)
	}
	if len(cfg.SkippedPlugins) != 1 || cfg.SkippedPlugins[0] != "aws" {
		t.Errorf("skipped = %v, want [aws]", cfg.SkippedPlugins)
	}
}

func TestSelectPluginsForStage_Disabled(t *testing.T) {
	cfg := &ast.DeployFile{
		Stage: "production",
		Plugins: []ast.PluginSpec{
			{Name: "aws"},                          // enabled by default
			{Name: "datadog", Enabled: new(false)}, // explicitly disabled
		},
	}
	selectPluginsForStage(cfg)
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].Name != "aws" {
		t.Errorf("active plugins = %+v, want [aws]", cfg.Plugins)
	}
	if len(cfg.SkippedPlugins) != 1 || cfg.SkippedPlugins[0] != "datadog" {
		t.Errorf("skipped = %v, want [datadog]", cfg.SkippedPlugins)
	}
}
