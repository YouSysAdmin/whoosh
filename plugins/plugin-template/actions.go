package plugintemplate

// Action tasks: a task invokes an action by its global name (`action: plugin-template:render`) with per-call params
// (`with:`). The ActionFunc runs OPERATOR-SIDE, once - not per host, and never under --dry-run (the executor prints
// the planned call instead).
//
// To reach the deploy hosts, the executor puts two bridges in the action's ctx, both bound to the hosts the action
// task targets (roles:, --host, ...):
//
//	whoosh.HostFileWriterFrom(ctx)    - write a generated file on every target host (0600, relative = release dir)
//	whoosh.HostCommandRunnerFrom(ctx) - run a command on every target host (parallel, fail-fast, echoed per host)
//
// Both are nil outside the executor (unit tests, `whoosh validate`) - fail with a clear error, never silently skip.
// Progress goes to `out` (masked, host-prefixed, captured for --log-file), never to os.Stdout.

import (
	"context"
	"fmt"
	"io"

	"github.com/yousysadmin/whoosh"
)

const (
	// ActionRender fetches a value operator-side (once) and renders it as a file on every target host - the
	// HostFileWriter pattern (compare aws:ssm:to-dotenv).
	ActionRender = pluginName + ":render"
	// ActionExec runs a command on every target host - the HostCommandRunner pattern (compare the systemd plugin).
	ActionExec = pluginName + ":exec"
)

// renderParams is ActionRender's call surface: the task's `with:`, layered over the `actions:` entry's defaults.
type renderParams struct {
	// Key selects what to fetch from the external system.
	Key string `yaml:"key"`
	// Path is where the file lands on each host, a relative path resolves against the release dir.
	Path string `yaml:"path"`
}

// render is the ActionFunc behind `action: plugin-template:render`.
// TODO: replace the fetch+format with your real content generation, keep the fetch-once / write-per-host shape.
func (p *plugin) render(ctx context.Context, with map[string]any, out io.Writer) error {
	var a renderParams
	if err := decodeFeature(p.features[ActionRender], with, &a); err != nil {
		return fmt.Errorf("%s: %w", ActionRender, err)
	}
	if a.Key == "" || a.Path == "" {
		return fmt.Errorf("%s: both 'key' and 'path' are required", ActionRender)
	}
	if err := p.client.requireEndpoint(ActionRender); err != nil {
		return err
	}

	// Fetch ONCE, operator-side. Register anything sensitive so whoosh masks it everywhere.
	value, err := p.client.fetch(ctx, a.Key)
	if err != nil {
		return fmt.Errorf("%s: fetch %q: %w", ActionRender, a.Key, err)
	}
	whoosh.AddSecret(value)

	w := whoosh.HostFileWriterFrom(ctx)
	if w == nil {
		return fmt.Errorf("%s: no host file writer in context (the action must run as a whoosh task)", ActionRender)
	}
	fmt.Fprintf(out, "%s: rendering %s\n", ActionRender, a.Path)
	return w.WriteFile(ctx, a.Path, []byte(value+"\n"))
}

// execParams is ActionExec's call surface.
type execParams struct {
	// Cmd is the shell command to run on every target host. It comes from the operator's own Deployfile, so it is
	// trusted config - but anything the PLUGIN interpolates into a command must be validated or quoted first (see the
	// systemd plugin's unit-name regex for the pattern).
	Cmd string `yaml:"cmd"`
}

// exec is the ActionFunc behind `action: plugin-template:exec`.
func (p *plugin) exec(ctx context.Context, with map[string]any, _ io.Writer) error {
	var a execParams
	if err := decodeFeature(p.features[ActionExec], with, &a); err != nil {
		return fmt.Errorf("%s: %w", ActionExec, err)
	}
	if a.Cmd == "" {
		return fmt.Errorf("%s: 'cmd' is required", ActionExec)
	}
	r := whoosh.HostCommandRunnerFrom(ctx)
	if r == nil {
		return fmt.Errorf("%s: no host command runner in context (the action must run as a whoosh task)", ActionExec)
	}
	// The runner echoes the command per host itself - no extra progress line needed.
	return r.RunCommand(ctx, a.Cmd)
}
