// Package hello is a standalone example of an out-of-tree whoosh plugins - a template to copy for your own
// private/third-party plugins.
// It lives in its own module (see go.mod) and depends only on whoosh's public API (github.com/yousysadmin/whoosh),
// never on whoosh's internal packages.
//
// Build a whoosh binary that includes it with `whoosh build`:
//
//	whoosh build --with github.com/yousysadmin/whoosh-example-hello -o ./whoosh
//
// or, against a local checkout of both:
//
//	whoosh build \
//	  --replace github.com/yousysadmin/whoosh=/path/to/whoosh \
//	  --with github.com/yousysadmin/whoosh-example-hello \
//	  --replace github.com/yousysadmin/whoosh-example-hello=/path/to/whoosh/examples/plugins/hello \
//	  -o ./whoosh
//
// Then use it from a Deployfile:
//
//	plugins:
//	  - name: hello
//	    params:
//	      greeting: "Hi"          # global param, shared by the plugin's actions
//
//	tasks:
//	  greet:
//	    action: hello:greet       # an action task (operator-side, no host targeting)
//	    with:
//	      name: "{{ .app_name }}" # with: values are templated first
//
// Run it with `whoosh <stage> greet`.
package hello

import (
	"context"
	"fmt"
	"io"

	"github.com/yousysadmin/whoosh"
)

// pluginName is the registered name (the Deployfile `plugins: - name:` value) and the namespace prefix of the actions
// this plugins registers ("hello:...").
const pluginName = "hello"

// actionGreet is the global name of the one action this plugins contributes, invoked by a task's `action: hello:greet`.
const actionGreet = "hello:greet"

// init registers the plugins with whoosh so a custom build that blank-imports this module makes it available.
// Every plugins package does this.
func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &helloPlugin{} })
}

// params are the plugin's global params (the `params:` block).
type params struct {
	Greeting string `yaml:"greeting"`
}

// greetParams are the per-task params for hello:greet (the task's `with:` block).
type greetParams struct {
	Name string `yaml:"name"`
}

// helloPlugin holds whatever the plugins needs at action time - here just the configured greeting.
type helloPlugin struct {
	greeting string
}

// Configure decodes the global params and registers the action(s). It runs once when the plugin loads.
// A plugins that discovers hosts would also call reg.AddStartup(fn) here.
func (h *helloPlugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var p params
	if err := whoosh.DecodeParams(spec.Params, &p); err != nil {
		return fmt.Errorf("hello params: %w", err)
	}
	h.greeting = p.Greeting
	if h.greeting == "" {
		h.greeting = "Hello"
	}
	return reg.AddAction(actionGreet, h.greet)
}

// greet is the action. raw are the task's already-templated `with:` values, it writes to out, which the executor
// redacts and routes to the console / log.
func (h *helloPlugin) greet(_ context.Context, raw map[string]any, out io.Writer) error {
	var p greetParams
	if err := whoosh.DecodeParams(raw, &p); err != nil {
		return fmt.Errorf("hello:greet params: %w", err)
	}
	name := p.Name
	if name == "" {
		name = "world"
	}
	fmt.Fprintf(out, "%s, %s!\n", h.greeting, name)
	return nil
}
