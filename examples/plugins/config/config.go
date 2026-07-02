// Package config is an example whoosh plugins showing how a plugin works with
// vars, secrets, and template/command imports through the public SDK:
//
//   - read a stage var          (cfg.Vars["..."])
//   - register a secret         (whoosh.AddSecret - masks it from all output)
//   - inject a template value   (cfg.AddImport - exposed as {{ .<ns>.<key> }} and $<NS>_<KEY>)
//   - an action that uses them  (reg.AddAction)
//
// Configure it from a Deployfile:
//
//	vars:
//	  environment: production
//	plugins:
//	  - name: example-config
//	    params:
//	      token: "{{ env \"API_TOKEN\" }}"   # a secret the plugins manages
//
// The fetched token is registered as a secret, so it is masked everywhere whoosh prints (echoed commands, output, logs).
// Other tasks can read the injected values as {{ .example.token }} / $EXAMPLE_TOKEN and {{ .example.environment }}.
package config

import (
	"context"
	"fmt"
	"io"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName = "example-config"
	namespace  = "example"
	actionShow = "example-config:show"
)

func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &configPlugin{} })
}

// params are the plugin's global params (the `params:` block).
type params struct {
	Token string `yaml:"token"`
}

type configPlugin struct {
	token string
}

// Configure decodes params, registers a startup hook (injects vars/secrets/imports) and an action (prints the resolved value).
func (p *configPlugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var cp params
	if err := whoosh.DecodeParams(spec.Params, &cp); err != nil {
		return fmt.Errorf("%s params: %w", pluginName, err)
	}
	p.token = cp.Token
	reg.AddStartup(p.inject)
	return reg.AddAction(actionShow, p.show)
}

// inject runs once at load against the resolved config.
func (p *configPlugin) inject(_ context.Context, cfg *whoosh.DeployFile) error {
	if p.token != "" {
		// Mask the value everywhere whoosh prints it, then expose it to templates and commands as {{ .example.token }} /
		// $EXAMPLE_TOKEN.
		whoosh.AddSecret(p.token)
		cfg.AddImport(namespace, "token", p.token)
	}
	// Read a stage var (set under `vars:` in the Deployfile) and re-expose it under our namespace,
	// so tasks can use {{ .example.environment }} / $EXAMPLE_ENVIRONMENT.
	if env, ok := cfg.Vars["environment"].(string); ok {
		cfg.AddImport(namespace, "environment", env)
	}
	return nil
}

// show is the action (run via `action: example-config:show`).
// The token prints masked in real output because it was registered with AddSecret.
func (p *configPlugin) show(_ context.Context, _ map[string]any, out io.Writer) error {
	fmt.Fprintf(out, "[example-config] token=%s\n", p.token)
	return nil
}
