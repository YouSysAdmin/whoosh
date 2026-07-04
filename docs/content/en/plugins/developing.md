---
title: "Developing"
description: "Author your own whoosh plugins: the Configure contract, actions, startup hooks (tasks, hooks, custom phases, inventory, imports), secrets, host-file writing, testing, and building a custom binary with whoosh build."
weight: 100
---

A plugin extends whoosh from Go: it can add **actions** (operator-side steps a task invokes by name) and a **startup
hook** (run once at load, which may mutate the resolved config - append inventory hosts, add tasks/hooks/custom
phases, inject template values and secrets). Plugins are **compiled in** - there's no runtime loading.
The bundled `print-hosts-table` built-in and the separate `aws`, `slack`, and `rbenv` plugin modules all use this
exact contract, and nothing here is private to the core.

This page is the authoring reference. For *using* the plugins, see the [Plugins overview](/plugins/overview/).

## The contract

A plugin is a small Go module that:

1. imports **only** the public API `github.com/yousysadmin/whoosh` (never whoosh's `internal/...` packages),
2. registers itself in `init()` with `whoosh.Register(name, factory)`, and
3. implements the one-method `whoosh.Plugin` interface:

```go
Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error
```

`Configure` runs once when the plugin loads.
It validates the plugin's spec (global `params:` + per-action config) and registers what the plugin contributes into
`reg`.
The core only ever: runs startup hooks, looks up actions by name, and applies the stage filter - it never references a
specific plugin.

```go
package hello

import (
	"context"
	"fmt"
	"io"

	"github.com/yousysadmin/whoosh"
)

func init() { whoosh.Register("hello", func() whoosh.Plugin { return &plugin{} }) }

type plugin struct{ greeting string }

type params struct {
	Greeting string `yaml:"greeting"` // the plugin's `params:` block
}

func (p *plugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var pp params
	if err := whoosh.DecodeParams(spec.Params, &pp); err != nil {
		return fmt.Errorf("hello params: %w", err)
	}
	p.greeting = pp.Greeting
	if p.greeting == "" {
		p.greeting = "Hello"
	}
	// reg.AddStartup(p.install)     // optionally mutate the config at load
	return reg.AddAction("hello:greet", p.greet)
}

func (p *plugin) greet(_ context.Context, with map[string]any, out io.Writer) error {
	// `with` is the task's already-templated `with:` map.
	fmt.Fprintf(out, "%s, %v!\n", p.greeting, with["name"])
	return nil
}
```

Use it from a Deployfile once the plugin is built into the binary:

```yaml
plugins:
  - name: hello
    params: { greeting: "Hi" }
tasks:
  greet:
    action: hello:greet
    with: { name: "{{ .app_name }}" }   # with: values are templated first
```

{{< callout type="note" >}}
**Naming convention.**
Use the plugin name as the namespace for its actions - `hello` registers `hello:greet`, `aws` registers
`aws:ec2:asg:refresh`, etc. The core's per-stage gate keys off the segment **before the first colon**, so every
`hello:*` action is enabled/disabled together with the `hello` plugin.
{{< /callout >}}

## Params

Whoosh hands you untyped maps.
Turn them into a struct with `whoosh.DecodeParams` (a YAML round-trip, so use ordinary `yaml:"..."` tags):

- **Global `params:`** arrive as `spec.Params` in `Configure`.
  Decode them once and stash what you need on the plugin struct.
- **Per-task `with:`** arrives as the `params map[string]any` argument to your action. Decode it per call.

```go
var p withParams
if err := whoosh.DecodeParams(with, &p); err != nil {
return fmt.Errorf("hello:greet params: %w", err)
}
```

**The core templates values before you see them.**
Plugin `params:` are rendered at load time (against `vars` + static config + sprig - no run-time values, since plugins
load before any release exists), and a task's `with:` is deep-templated (string values at any nesting depth) just
before your action runs. So by the time you decode, `"{{ .bastion }}"` is already `10.0.0.1`.
You don't render templates yourself.

For multi-feature ("umbrella") plugins, the `aws` plugin module also layers each feature's `actions[].params`
**under** a task's `with:` as defaults - that merge is implemented in the plugin, not the core.
See [the aws plugin page](/plugins/aws/) if you want to mirror it.

## Actions

An action is an `whoosh.ActionFunc`:

```go
func(ctx context.Context, params map[string]any, out io.Writer) error
```

Register it with `reg.AddAction("ns:verb", fn)` (duplicate names error). Key facts:

- **It runs operator-side**, once, on the machine running whoosh - not over SSH.
  There is no host, so `{{.host}}` in the task renders empty.
  (To act on the deploy hosts, contribute a *task* from a startup hook instead - see below - or use the executor-supplied
  bridges: write a file to them with the [host-file writer](#writing-files-on-the-tasks-hosts), or run a command on them
  with the [host-command runner](#running-commands-on-the-tasks-hosts).)
- **Write progress to `out`**, not directly to stdout. The executor wraps `out` with masking and host-prefixing.
- **`--dry-run` does not call your action** - the executor prints the planned call and skips it.
  So an action may assume it only runs for real.
- **Return an error to fail the task.** Wrap it with context (`fmt.Errorf("...: %w", err)`).
  The message surfaces to the operator and sets the deploy exit code.
- Respect `ctx` - honor `ctx.Done()` in any long poll so `Ctrl-C` is responsive.

A task invokes it with `action:`/`with:` (mutually exclusive with `cmds`/`scripts`).
See [Plugins -> action tasks](/plugins/overview/#action-tasks).

## Startup hooks

A startup hook is a `whoosh.StartupFunc`:

```go
func(ctx context.Context, cfg *whoosh.DeployFile) error
```

Register it with `reg.AddStartup(fn)`.
It runs **once at load**, for the stage being deployed, against the fully-resolved config - and may mutate `cfg`.
This is how a plugin adds to the deploy itself. Use the typed mutators rather than poking fields directly:

### Append inventory hosts

Dynamic inventory: append to `cfg.Hosts`. (This is what `aws:ec2:inventory` does.)

```go
func (p *plugin) discover(_ context.Context, cfg *whoosh.DeployFile) error {
for _, h := range p.lookup() {
cfg.Hosts = append(cfg.Hosts, whoosh.Host{
Address: h.IP,
Roles:   []string{"app"},
// Deploy/Required, etc. - see the Hosts reference.
})
}
return nil
}
```

### Add a task and wire it into a phase

`cfg.AddTask(name, *whoosh.Task)` contributes a task, and `cfg.AddHookBefore` / `cfg.AddHookAfter` run it around any
phase (built-in or custom).
A task's `Cmds` run on the deploy hosts over SSH (Go-templated against the deploy context), then its `Scripts`.
Set `Local: true` to run on the operator machine instead. Ship a script inside the binary with `//go:embed`:

```go
//go:embed healthcheck.sh
var healthcheckScript string

func (p *plugin) install(_ context.Context, cfg *whoosh.DeployFile) error {
cfg.AddTask("healthcheck", &whoosh.Task{
Desc: "Post-publish healthcheck",
Cmds: []string{`echo "{{.app_name}} live at {{.release_path}} on {{.host}}"`},
Scripts: []whoosh.Script{{Name: "healthcheck", Script: healthcheckScript}},
})
cfg.AddHookAfter("deploy:published", "healthcheck")
return nil
}
```

`AddHookBefore`/`AddHookAfter` are variadic (`phase string, tasks ...string`).
The built-in phase names are re-exported as constants (`whoosh.PhaseStarting` ... `whoosh.PhaseFinished`, plus
`whoosh.PhaseFailed`/`whoosh.PhaseRollback`), so `cfg.AddHookAfter(whoosh.PhasePublished, "healthcheck")` avoids
hardcoding the string.
See [Tasks](/configuration/tasks/) for the full `Task`/`Script` field set, and [Hooks](/configuration/hooks/) for the
phase names.

### Direct console output at a phase

To run your own Go code at a phase - typically to print operator-side output - register a **phase func-hook** instead
of contributing an `echo`-style task.
It receives the deploy's console writer (the same stream command output goes to):

```go
func (p *plugin) install(_ context.Context, cfg *whoosh.DeployFile) error {
cfg.AddHookFuncAfter("deploy:published", func (_ context.Context, out io.Writer) error {
fmt.Fprintf(out, "%s is live \n", cfg.App.Name) // closure captures cfg
return nil
})
return nil
}
```

`HookFunc` is `func(ctx context.Context, out io.Writer) error`, registered with
`cfg.AddHookFuncBefore`/`AddHookFuncAfter`. A returned error aborts the deploy like a failing task hook.
Func-hooks run **only during the deploy lifecycle** (not for `config`/`hosts`/`run`), after that phase's task hooks.
This is exactly how the bundled `print-hosts-table` plugin prints the hosts table before `deploy:starting`.

### Add a custom phase

`cfg.AddPhase(whoosh.CustomPhase{...})` splices a **named phase** into the deploy lifecycle, before or after a
built-in phase. It runs an optional task and is itself a `before`/`after` hook anchor:

```go
cfg.AddTask("run-migrations", &whoosh.Task{Cmds: []string{`echo "migrating {{.app_name}} in {{.phase}}"`}})
cfg.AddPhase(whoosh.CustomPhase{
Name:  "deploy:migrate",
After: "deploy:published", // anchor - set EXACTLY ONE of Before/After to a built-in phase
Task:  "run-migrations", // optional, omit for a pure hook anchor
})
```

Rules (validated when the deploy starts): the anchor (`Before`/`After`) must be a built-in phase, the name must be
unique and not collide with a built-in, and the named `Task` must exist.
The task can branch on the phase via `{{.phase}}` / `$DEPLOY_PHASE`.
A Deployfile can declare the same thing under `custom_phases:` without a plugin - see [Plugins -> custom
phases](/plugins/overview/#custom-phases).

### Inject template/command values (imports)

`cfg.AddImport(ns, key, val)` exposes a value to **every** task, command and script as `{{ .<ns>.<key> }}` (template)
and `$<NS>_<KEY>` (env var) - useful for config a plugin fetches at load.
Imports are runtime-only: they are **not** emitted by `whoosh <stage> config` and don't appear under `{{.config}}`.

```go
func (p *plugin) inject(_ context.Context, cfg *whoosh.DeployFile) error {
if p.token != "" {
whoosh.AddSecret(p.token) // mask it everywhere (see below)
cfg.AddImport("example", "token", p.token) // -> {{ .example.token }} / $EXAMPLE_TOKEN
}
if env, ok := cfg.Vars["environment"].(string); ok { // read a stage var
cfg.AddImport("example", "environment", env)
}
return nil
}
```

{{< callout type="note" >}}
Template field access needs a valid identifier - for a key with dashes use `{{ index .example "has-dashes" }}`.
The env form is always normalized (`$EXAMPLE_HAS_DASHES`).
{{< /callout >}}

## Secrets and masking

If your plugin fetches or handles a secret, register the literal with `whoosh.AddSecret(value)`.
Whoosh then redacts every occurrence from echoed commands, command output, logs, and dry-run plans
(longest-match-first, minimum length 4).
Do this for anything sensitive you inject as an import or pass into a command.
In tests, `whoosh.Masking(s)` applies the same transform so you can assert a value is masked.

## Writing files on the task's hosts

An action runs operator-side, but sometimes you fetch something **once** (an API call) and need to render it as a file
on **each** host the task targets - e.g. an `.env` from a secrets store.
The executor puts a `whoosh.HostFileWriter` in the action's `ctx` - retrieve it with `whoosh.HostFileWriterFrom(ctx)`:

```go
func (p *plugin) writeEnv(ctx context.Context, with map[string]any, out io.Writer) error {
content := p.fetchOnce() // operator-side, once
if w := whoosh.HostFileWriterFrom(ctx); w != nil {
// Written to every host the task targets, a relative path resolves
// against the release dir, created 0600.
return w.WriteFile(ctx, ".env.local", content)
}
return os.WriteFile(".env.local", content, 0o600) // fallback when there's no executor context (e.g. tests)
}
```

This is exactly how `aws:ssm:to-dotenv` / `aws:secrets:to-dotenv` work: one operator-side fetch, the file rendered per
host.
Pick the hosts with the task's `roles:`, and run it from a hook after `deploy:updated` so the release dir exists.

## Running commands on the task's hosts

The command counterpart to the host-file writer: the executor also puts a `whoosh.HostCommandRunner` in the action's
`ctx` - retrieve it with `whoosh.HostCommandRunnerFrom(ctx)`:

```go
func (p *plugin) restart(ctx context.Context, with map[string]any, out io.Writer) error {
	r := whoosh.HostCommandRunnerFrom(ctx)
	if r == nil {
		return fmt.Errorf("no host command runner in context (the action must run as a whoosh task)")
	}
	// Runs on every host the task targets, in parallel, fail-fast - echoed per host
	// to the (redacted) command stream like a task cmd.
	return r.RunCommand(ctx, "systemctl restart 'app'")
}
```

The command runs verbatim (no release-dir `cd`, no task env preamble), so use absolute paths or self-contained
commands, and **quote or validate anything you interpolate** - the string reaches a shell on every host.
This is how the bundled [`systemd`](/plugins/systemd/) plugin runs `systemctl` on the deploy hosts.

## Default-on plugins

Register with `whoosh.RegisterDefault(name, factory)` instead of `Register` to make a plugin **always-on**: it loads
in every stage without a `plugins:` entry.
A Deployfile can still turn it off by listing it disabled (`enabled: false`, or an `only`/`except` that excludes the
stage) - a declared spec always wins. This is for zero-config convenience plugins.
`print-hosts-table` is one bundled example: its startup hook registers a func-hook that prints the hosts table before
`deploy:starting`, and it contributes the `deploy:hosts` CLI command (see below). [`systemd`](/plugins/systemd/) is
another: its actions are usable from any task with zero config.

```go
func init() { whoosh.RegisterDefault("print-hosts-table", func () whoosh.Plugin { return &plugin{} }) }
```

## Adding CLI commands

A plugin can also contribute `whoosh <stage> <name>` subcommands by implementing the optional `whoosh.Commander`
interface:

```go
func (p *plugin) Commands() []whoosh.Command {
	return []whoosh.Command{{
		Name:  "hello:info",
		Short: "Print what the hello plugin resolved",
		Run: func(_ context.Context, cfg *whoosh.DeployFile, _ *whoosh.Registry, out io.Writer, _ []string) error {
			fmt.Fprintf(out, "%d hosts for %s\n", len(cfg.Hosts), cfg.App.Name)
			return nil
		},
	}}
}
```

- `Commands()` is queried on a **bare instance** - no `Configure`, no network - so the CLI can register the
  subcommands offline and keep `--help`/completion fast. Return static declarations only.
- `Run` receives the **fully-loaded config** (startup hooks already ran, so dynamic inventory is resolved), the
  populated registry (to invoke a registered action if needed), the console writer, and the positional args.
- Built-in stage actions and Deployfile tasks win on a name collision.

This is how the bundled `print-hosts-table` plugin provides `whoosh <stage> deploy:hosts`.

## Reporting a version

Implement the optional `whoosh.Versioner` interface to have your plugin's version shown by `whoosh plugins` and
`whoosh version`:

```go
const pluginVersion = "1.0.0"

func (p *plugin) Version() string { return pluginVersion }
```

It's queried on a bare instance (no `Configure`, no network), so return a constant. A plugin that doesn't implement it
just shows no version. (`whoosh plugins` prints `name  version`, `whoosh version` appends `plugins: name version, ...`.)

## How the core gates your plugin

A Deployfile controls activation with `enabled:`, `only:`, and `except:` (see [Plugins ->
enabling](/plugins/overview/#enabling--disabling-a-plugin)).
The core applies all of this for you - there's nothing to implement:

- A disabled / stage-inactive plugin is simply **not loaded** (its `Configure`, startup hook, and actions never run),
  and any action task in its namespace is **skipped** (logged), not failed.
- So **do all work in `Configure`/startup hooks, never in `init()`** - `init()` runs at process start for every
  compiled-in plugin regardless of whether it's active. Keep `init()` to just `Register`.

## Testing a plugin

`whoosh.Load` builds a registry from specs without the CLI, so you can unit-test an action or a startup hook directly:

```go
func TestGreet(t *testing.T) {
reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: "hello", Params: map[string]any{"greeting": "Hi"}}})
if err != nil { t.Fatal(err) }

fn, ok := reg.Action("hello:greet")
if !ok { t.Fatal("action not registered") }

var buf bytes.Buffer
if err := fn(context.Background(), map[string]any{"name": "world"}, &buf); err != nil {
t.Fatal(err)
}
if got := buf.String(); got != "Hi, world!\n" {
t.Fatalf("got %q", got)
}
}

func TestInstall(t *testing.T) {
reg, _ := whoosh.Load([]whoosh.PluginSpec{{Name: "example-pipeline"}})
cfg := &whoosh.DeployFile{}
if err := reg.RunStartup(context.Background(), cfg); err != nil { t.Fatal(err) }
if cfg.Tasks["example-healthcheck"] == nil {
t.Fatal("startup hook did not add the task")
}
}
```

(No `HostFileWriter` or `HostCommandRunner` is present in such a context - an action that writes host files should
fall back to a local write, and a command-running action should return a clear error or take a fake runner in tests -
see above.)

## Building a binary with your plugin

Plugins compile in.
The `whoosh build` command composes a custom binary from the standard plugins plus your `--with` modules (it needs the
Go toolchain on `PATH`):

```sh
whoosh build \
  --with github.com/acme/whoosh-datadog \
  --with github.com/acme/private-plugins@v1.2.0 \
  -o ./whoosh

./whoosh plugins        # lists the compiled-in plugins
```

| Flag                            | Meaning                                                                                                                                    |
|---------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `--with module[@version]`       | A plugin module to include (repeatable).                                                                                                   |
| `--replace old=path`            | Build a module from a local checkout (repeatable). Also how you build against a local whoosh: `--replace github.com/yousysadmin/whoosh=.`. |
| `-o, --output`                  | Output binary path (default `whoosh`).                                                                                                     |
| `--whoosh-version`              | The whoosh version to build against (default `latest`).                                                                                    |
| `--app-version`                 | Version string embedded in the binary (default: `--whoosh-version`).                                                                       |
| `--tags`                        | Extra go build tags for the compile.                                                                                                       |
| `--no-standard`                 | Omit the bundled (standard) plugins, including only `--with` modules.                                                                      |
| `--go` / `--keep` / `--verbose` | Go toolchain path / keep the temp build dir / print the go commands.                                                                       |

Building against a local checkout of both whoosh and your plugin:

```sh
whoosh build \
  --replace github.com/yousysadmin/whoosh=/path/to/whoosh \
  --with github.com/acme/myplugin \
  --replace github.com/acme/myplugin=/path/to/myplugin \
  -o ./whoosh
```

Private modules use your normal Go auth (`GOPRIVATE` + `~/.netrc` or SSH `insteadOf`), and you can cross-compile by
setting `GOOS`/`GOARCH`.
`whoosh <stage> validate` confirms a Deployfile's plugin names are compiled in and their param templates render.

## Public API reference

Everything you need is in `github.com/yousysadmin/whoosh`:

| Symbol                                                                                        | Purpose                                                                                                                                  |
|-----------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------|
| `Register(name, Factory)` / `RegisterDefault(name, Factory)`                                  | Self-register in `init()`. `RegisterDefault` is always-on.                                                                               |
| `Plugin`                                                                                      | The interface: `Configure(PluginSpec, *Registry) error`.                                                                                 |
| `Versioner`                                                                                   | Optional: `Version() string` - reports the plugin's version for `whoosh plugins` / `whoosh version`.                                     |
| `Commander` / `Command` / `CommandFunc`                                                       | Optional: `Commands() []Command` - contribute `whoosh <stage> <name>` CLI subcommands.                                                   |
| `Factory`                                                                                     | `func() Plugin` - builds an unconfigured instance.                                                                                       |
| `Registry`                                                                                    | `AddAction(name, ActionFunc) error`, `AddStartup(StartupFunc)`, `Action(name) (ActionFunc, bool)`, `RunStartup(ctx, *DeployFile) error`. |
| `ActionFunc`                                                                                  | `func(ctx, params map[string]any, out io.Writer) error`.                                                                                 |
| `StartupFunc`                                                                                 | `func(ctx, cfg *DeployFile) error`.                                                                                                      |
| `DecodeParams(map[string]any, target) error`                                                  | Untyped params -> a typed struct (YAML tags).                                                                                            |
| `PluginSpec` / `PluginActionSpec`                                                             | The Deployfile entry (`.Params`, `.Actions`).                                                                                            |
| `DeployFile`                                                                                  | The resolved config - mutate via `AddTask`, `AddHookBefore`/`AddHookAfter`, `AddHookFuncBefore`/`AddHookFuncAfter`, `AddPhase`, `AddImport`, and `cfg.Hosts`/`cfg.Vars`. |
| `Task` / `Script` / `Host` / `Hooks` / `CustomPhase`                                          | Config types you construct.                                                                                                              |
| `HookFunc`                                                                                    | `func(ctx, out io.Writer) error` - a phase func-hook (see above).                                                                        |
| `PhaseStarting` ... `PhaseFinished` / `PhaseFailed` / `PhaseRollback`                         | The built-in phase names as constants, for hook/phase anchors.                                                                           |
| `HostFileWriterFrom(ctx) HostFileWriter`                                                      | Render a file onto the task's hosts (`WriteFile(ctx, path, content)`).                                                                   |
| `HostCommandRunnerFrom(ctx) HostCommandRunner`                                                | Run a command on the task's hosts (`RunCommand(ctx, cmd)`), parallel and fail-fast.                                                      |
| `AddSecret(string)` / `Masking(string) string`                                                | Register a literal to redact / apply the same masking (tests).                                                                           |
| `Registered() []string` / `IsRegistered(name) bool` / `Load([]PluginSpec) (*Registry, error)` | Introspection and test harness.                                                                                                          |

The entrypoint (`Main`) lives in a separate package (`github.com/yousysadmin/whoosh/entrypoint`), so importing the SDK
does **not** pull in the CLI - keeping a plugin module light.

## Examples

Copy-ready starting points (each its own module, importing only the public API):

- [`plugins/plugin-template`](https://github.com/YouSysAdmin/whoosh/tree/master/plugins/plugin-template)
    - **the template to copy**: a compiling, tested stub exercising the full interface - params + offline validation,
      an action with both host bridges (command runner + file writer), a startup hook (task + phase hook, func-hook,
      imports), Versioner, and a CLI command.
- [`examples/plugins/hello`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/plugins/hello)
    - the minimal plugin: register a name, decode params, one action.
- [`examples/plugins`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/plugins)
    - focused examples: `pipeline` (add a task + embedded script, wire a hook),
      `config` (vars, `AddSecret`, `AddImport`, an action), and `phase` (a custom phase).
- [`plugins/slack`](https://github.com/YouSysAdmin/whoosh/tree/master/plugins/slack)
    - a compact real plugin: one action, config-driven notification tasks wired into phases, masking.
- [`plugins/rbenv`](https://github.com/YouSysAdmin/whoosh/tree/master/plugins/rbenv)
    - a contributed-task plugin: an embedded setup script, per-stage params, imports.
- [`plugins/aws`](https://github.com/YouSysAdmin/whoosh/tree/master/plugins/aws)
    - a full-featured plugin (shared clients, several features, startup + actions + a host-file writer).
