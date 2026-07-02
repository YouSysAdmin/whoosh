// Package whoosh is the public plugin SDK: it lets an out-of-tree module author a whoosh plugin (and lets the
// built-in plugins use the very same contract) without importing whoosh's internal packages.
// The binary entrypoint lives in a separate package (github.com/yousysadmin/whoosh/entrypoint) so importing the SDK
// does not pull in the CLI.
//
// The types here are aliases of whoosh's internal types, so a plugin written against this package is the exact same
// contract the built-in plugins use; both register into the same registry.
// To scaffold a plugin, copy the standalone example module under examples/plugins/hello, and build with `whoosh
// build`.
//
// # The two-phase plugin contract
//
// A plugin contributes in two moments, on two surfaces:
//
//   - Configure time (Plugin.Configure): the config is not resolved yet, so only the Registry is available -
//     reg.AddAction registers named actions, reg.AddStartup registers a startup hook. A typical Configure just
//     validates/decodes the PluginSpec params and defers everything else to a startup closure.
//   - Startup time (the StartupFunc): the resolved *DeployFile is passed in and may be mutated - cfg.AddTask,
//     cfg.AddHookBefore/After, cfg.AddHookFuncBefore/After, cfg.AddPhase, cfg.AddImport, or appending to cfg.Hosts
//     (inventory). These methods are startup-time only; there is no config to mutate at Configure time.
//
// A plugin's Version() (the Versioner interface) is self-declared and unrelated to the whoosh binary's version.
package whoosh

import (
	"context"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/plugins"
)

// Registered returns the names of every plugin compiled into this binary, sorted.
// Mirrors what the `whoosh plugins` command prints.
func Registered() []string { return plugins.Registered() }

// The plugin SDK.
// These are aliases of the internal types, so a value produced by whoosh's core (e.g. the PluginSpec passed to
// Configure) is identical to the one a plugin declares against this package - no conversion, and both in-tree and
// out-of-tree plugins satisfy the same Plugin interface.
type (
	// Plugin is the single contract every plugin satisfies; implement Configure.
	Plugin = plugins.Plugin
	// Factory builds an unconfigured plugin instance (what you pass to Register).
	Factory = plugins.Factory
	// Registry is the shared space a plugin registers its actions/startup into.
	Registry = plugins.Registry
	// ActionFunc is a named action invoked by a task's `action:`/`with:`.
	ActionFunc = plugins.ActionFunc
	// StartupFunc runs once at load and may mutate the resolved config.
	StartupFunc = plugins.StartupFunc
	// HostFileWriter renders a generated file onto an action task's hosts.
	HostFileWriter = plugins.HostFileWriter

	// PluginSpec is the plugin's Deployfile entry (params + per-action config).
	PluginSpec = ast.PluginSpec
	// PluginActionSpec is one entry of PluginSpec.Actions.
	PluginActionSpec = ast.PluginActionSpec
	// DeployFile is the resolved config a StartupFunc receives (and may mutate, e.g. cfg.AddTask / cfg.AddHookAfter /
	// cfg.AddPhase / cfg.AddImport).
	DeployFile = ast.DeployFile
	// Host is an inventory host; an inventory plugin appends these in startup.
	Host = ast.Host
	// Task is a pipeline task a plugin can contribute via DeployFile.AddTask: it runs cmds and/or scripts on the targets
	// (or locally), or invokes an action.
	Task = ast.Task
	// Script is one script entry of a Task (inline content or a file path).
	Script = ast.Script
	// Hooks maps a phase name to the task names run before/after it.
	Hooks = ast.Hooks
	// CustomPhase is a named phase a plugin can splice into the deployment lifecycle before/after a built-in phase via
	// DeployFile.AddPhase.
	CustomPhase = ast.CustomPhase
	// HookFunc is a plugin function run before/after a deploy phase with the deploy's console writer, registered via
	// DeployFile.AddHookFuncBefore/After.
	// It lets a plugin emit operator-side output (or run code) at a phase without contributing a task.
	// Runs only during the deploy lifecycle.
	HookFunc = ast.HookFunc

	// Command is a CLI subcommand a plugin contributes (`whoosh <stage> <Name>`), declared via the Commander interface.
	Command = plugins.Command
	// CommandFunc runs a plugin Command with the resolved config, the registry, the console writer, and the command's
	// positional args.
	CommandFunc = plugins.CommandFunc
	// Commander is the optional interface a plugin implements to contribute CLI commands; Commands() is queried (on a
	// bare instance) to register them.
	Commander = plugins.Commander
	// Versioner is the optional interface a plugin implements to report its version, shown by `whoosh plugins` /
	// `whoosh version`. Implement `Version() string`.
	Versioner = plugins.Versioner
)

// Built-in deploy phase names, in lifecycle order - the hook anchors a plugin targets with cfg.AddHookBefore/After,
// cfg.AddHookFuncBefore/After, or as a cfg.AddPhase anchor. Re-exported so a plugin never hardcodes the strings.
// PhaseFailed (after-only, fires on a failed deploy) and PhaseRollback (wraps the rollback swap) are hook points
// outside the lifecycle order.
const (
	PhaseStarting   = ast.PhaseStarting
	PhaseCheck      = ast.PhaseCheck
	PhaseInit       = ast.PhaseInit
	PhaseStarted    = ast.PhaseStarted
	PhaseUpdating   = ast.PhaseUpdating
	PhaseSymlink    = ast.PhaseSymlink
	PhaseUpdated    = ast.PhaseUpdated
	PhasePublishing = ast.PhasePublishing
	PhasePublished  = ast.PhasePublished
	PhaseFinishing  = ast.PhaseFinishing
	PhaseFinished   = ast.PhaseFinished
	PhaseFailed     = ast.PhaseFailed
	PhaseRollback   = ast.PhaseRollback
)

// HostSourceConfig is the Host.Source value for a host declared in the Deployfile; an inventory plugin sets its own
// source string (conventionally its feature name, e.g. "aws:ec2:inventory") on the hosts it appends.
const HostSourceConfig = ast.HostSourceConfig

// Register makes a plugin available under name. Call it from a plugin package's init(); duplicate names panic.
func Register(name string, f Factory) { plugins.Register(name, f) }

// RegisterDefault is like Register but also marks the plugin always-on: it loads in every stage unless a Deployfile
// lists it disabled (enabled: false, or only/except excluding the stage). For zero-config convenience plugins.
func RegisterDefault(name string, f Factory) { plugins.RegisterDefault(name, f) }

// IsRegistered reports whether a plugin with this name is compiled in.
func IsRegistered(name string) bool { return plugins.IsRegistered(name) }

// Load configures every declared plugins and returns the populated registry.
// Mainly for tests: build a registry, then invoke an action via reg.Action(name).
func Load(specs []PluginSpec) (*Registry, error) { return plugins.Load(specs) }

// DecodeParams maps an untyped params map into a typed struct via a YAML round trip, so a plugin can use ordinary
// structs with yaml tags.
func DecodeParams(params map[string]any, target any) error {
	return plugins.DecodeParams(params, target)
}

// WithHostFileWriter returns ctx carrying w (the executor sets this before an action runs).
// Plugin authors rarely call this; use HostFileWriterFrom.
func WithHostFileWriter(ctx context.Context, w HostFileWriter) context.Context {
	return plugins.WithHostFileWriter(ctx, w)
}

// HostFileWriterFrom returns the HostFileWriter carried by an action's ctx, or nil if none (e.g. an action invoked
// outside the executor).
func HostFileWriterFrom(ctx context.Context) HostFileWriter {
	return plugins.HostFileWriterFrom(ctx)
}

// AddSecret registers a literal value so whoosh redacts it from all output (echoed commands, command output, logs,
// dry-run plans). Use it for any secret a plugin fetches. Re-exports the internal masking registry.
func AddSecret(s string) { masking.AddSecret(s) }

// Masking returns s with every registered secret and known secret pattern masked
// - the same transform whoosh applies to its output. Useful in a plugin's tests
// to assert a value passed to AddSecret is masked.
func Masking(s string) string { return masking.String(s) }
