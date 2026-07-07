// Package plugins is whoosh's plugins framework. Plugins are self-contained: the
// core never references a specific plugins or its internals.
// Instead, each plugin registers itself by name (from an init function), and the Deployfile's plugins:
// list selects which to load. On load a plugin is given its params to validate, and it registers what it contributes
// into a Registry:
//
//   - a startup hook - runs once at load and may mutate the resolved config (e.g. append discovered hosts to Servers),
//     or
//   - named actions - invoked by a task or hook via their global name.
//
// The core only drives generic extension points (run the startup hooks, look up
// an action by name), it has no knowledge of what any plugin does.
package plugins

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"gopkg.in/yaml.v3"
)

// ActionFunc runs a named action invoked from a task or hook. params are the task's `with:` values, logs stream to out.
type ActionFunc func(ctx context.Context, params map[string]any, out io.Writer) error

// HostFileWriter renders a file onto the hosts an action task targets.
// An action fetches/builds content operator-side (once), then writes it to each host - bridging an operator-side action
// (e.g. an AWS API call) with a per-host file (e.g. an env file from SSM).
// The executor supplies it via the action's context, retrieve it with HostFileWriterFrom. path is resolved against the
// task's directory (the release dir) when relative.
type HostFileWriter interface {
	WriteFile(ctx context.Context, path string, content []byte) error
}

type hostWriterKey struct{}

// WithHostFileWriter returns ctx carrying w.
// The executor calls this before invoking an action so the action can render files on the task's hosts.
func WithHostFileWriter(ctx context.Context, w HostFileWriter) context.Context {
	return context.WithValue(ctx, hostWriterKey{}, w)
}

// HostFileWriterFrom returns the HostFileWriter carried by ctx, or nil if none (e.g. an action invoked outside the
// executor, as in a unit test).
func HostFileWriterFrom(ctx context.Context) HostFileWriter {
	w, _ := ctx.Value(hostWriterKey{}).(HostFileWriter)
	return w
}

// HostCommandRunner runs a shell command on the hosts an action task targets - the command counterpart to HostFileWriter.
// The command runs on every target host in parallel (fail-fast, like a task cmd), echoed per host to the redacted command stream.
// The executor supplies it via the action's context, retrieve it with HostCommandRunnerFrom.
type HostCommandRunner interface {
	RunCommand(ctx context.Context, cmd string) error
}

type hostRunnerKey struct{}

// WithHostCommandRunner returns ctx carrying r.
// The executor calls this before invoking an action so the action can run commands on the task's hosts.
func WithHostCommandRunner(ctx context.Context, r HostCommandRunner) context.Context {
	return context.WithValue(ctx, hostRunnerKey{}, r)
}

// HostCommandRunnerFrom returns the HostCommandRunner carried by ctx, or nil if none (e.g. an action invoked outside
// the executor, as in a unit test).
func HostCommandRunnerFrom(ctx context.Context) HostCommandRunner {
	r, _ := ctx.Value(hostRunnerKey{}).(HostCommandRunner)
	return r
}

// HostCommandCapturer runs a shell command on ONE of the hosts an action task targets (the first) and returns its
// trimmed stdout - the capture counterpart to HostCommandRunner, for actions that need a value off a host (e.g. a git
// log from the repo mirror) rather than a fanned-out side effect.
// The executor supplies it via the action's context, retrieve it with HostCommandCapturerFrom.
type HostCommandCapturer interface {
	CaptureCommand(ctx context.Context, cmd string) (string, error)
}

type hostCapturerKey struct{}

// WithHostCommandCapturer returns ctx carrying c.
// The executor calls this before invoking an action so the action can capture command output from a task host.
func WithHostCommandCapturer(ctx context.Context, c HostCommandCapturer) context.Context {
	return context.WithValue(ctx, hostCapturerKey{}, c)
}

// HostCommandCapturerFrom returns the HostCommandCapturer carried by ctx, or nil if none (e.g. an action invoked
// outside the executor, as in a unit test).
func HostCommandCapturerFrom(ctx context.Context) HostCommandCapturer {
	c, _ := ctx.Value(hostCapturerKey{}).(HostCommandCapturer)
	return c
}

// StartupFunc runs once at load and may mutate the resolved config - for example an inventory plugin appending to
// cfg.Hosts.
type StartupFunc func(ctx context.Context, cfg *ast.DeployFile) error

// Plugin is the single contract every plugin satisfies.
// Configure validates the plugin's spec (global params + per-action config) and registers its startup hooks and/or
// actions into reg.
type Plugin interface {
	Configure(spec ast.PluginSpec, reg *Registry) error
}

// Versioner is the optional interface a plugin implements to report its version, shown by `whoosh plugins` and `whoosh version`.
// Like Commander, it is queried on a bare (unconfigured) instance - no Configure, no network - so a  plugin that doesn't
// implement it simply reports an empty version.
type Versioner interface {
	Version() string
}

// Factory builds an unconfigured plugin instance.
type Factory func() Plugin

var factories = map[string]Factory{}

// Register makes a plugin available under name.
// Meant to be called from a plugin package's init, duplicate names panic.
func Register(name string, f Factory) {
	if _, dup := factories[name]; dup {
		panic("plugins already registered: " + name)
	}
	factories[name] = f
}

// defaultPlugins names plugins registered via RegisterDefault, in registration order.
// They load in every stage even when a Deployfile doesn't list them.
var defaultPlugins []string

// RegisterDefault is like Register but also marks the plugin always-on: it loads in every stage unless a Deployfile
// lists it disabled (enabled: false, or only/except that exclude the stage).
// For zero-config convenience plugins (e.g. printing the inventory). Meant to be called from a plugin package's init.
func RegisterDefault(name string, f Factory) {
	Register(name, f)
	defaultPlugins = append(defaultPlugins, name)
}

// DefaultSpecs returns listed with a bare spec appended for every default-on plugins (RegisterDefault) not already
// named in it, so always-on plugins load without being declared.
// A listed spec always wins - so enabled:false / only / except on a default plugins are honored - and the order of the
// listed specs is preserved.
func DefaultSpecs(listed []ast.PluginSpec) []ast.PluginSpec {
	have := make(map[string]bool, len(listed))
	for _, s := range listed {
		have[s.Name] = true
	}
	out := append([]ast.PluginSpec{}, listed...)
	for _, name := range defaultPlugins {
		if !have[name] {
			out = append(out, ast.PluginSpec{Name: name})
		}
	}
	return out
}

// IsRegistered reports whether a plugin with this name is compiled into the binary.
// Lets the validate command flag an unknown plugin name without loading (and connecting through) the plugins.
func IsRegistered(name string) bool {
	_, ok := factories[name]
	return ok
}

// Registered returns the names of every plugin compiled into this binary, sorted.
// Lets a custom build report what it contains (see the `plugins` command).
func Registered() []string {
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PluginInfo is a compiled-in plugin's name and version (version empty when the plugin doesn't implement Versioner).
type PluginInfo struct {
	Name    string
	Version string
}

// RegisteredInfo returns every plugin compiled into this binary with its version, sorted by name. The version is read
// from a bare instance via the optional Versioner interface (no Configure, no network), like command discovery.
func RegisteredInfo() []PluginInfo {
	out := make([]PluginInfo, 0, len(factories))
	for name, f := range factories {
		info := PluginInfo{Name: name}
		if v, ok := f().(Versioner); ok {
			info.Version = v.Version()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Registry is the shared space plugins register into and the core looks up.
type Registry struct {
	actions  map[string]ActionFunc
	startups []StartupFunc
}

func newRegistry() *Registry { return &Registry{actions: map[string]ActionFunc{}} }

// AddAction registers a globally-named action. Duplicate names are an error.
func (r *Registry) AddAction(name string, fn ActionFunc) error {
	if _, dup := r.actions[name]; dup {
		return fmt.Errorf("action %q already registered", name)
	}
	r.actions[name] = fn
	return nil
}

// AddStartup registers a hook to run at load.
func (r *Registry) AddStartup(fn StartupFunc) { r.startups = append(r.startups, fn) }

// Action looks up a registered action by name.
func (r *Registry) Action(name string) (ActionFunc, bool) {
	fn, ok := r.actions[name]
	return fn, ok
}

// RunStartup runs every startup hook in registration order.
func (r *Registry) RunStartup(ctx context.Context, cfg *ast.DeployFile) error {
	for _, fn := range r.startups {
		if err := fn(ctx, cfg); err != nil {
			return err
		}
	}
	return nil
}

// Load constructs and configures every declared plugins, returning the populated registry.
// Configuration validates params and performs registration, nothing runs yet.
func Load(specs []ast.PluginSpec) (*Registry, error) {
	reg := newRegistry()
	for _, spec := range specs {
		f, ok := factories[spec.Name]
		if !ok {
			return nil, fmt.Errorf("unknown plugin %q (not built into this binary)", spec.Name)
		}
		if err := f().Configure(spec, reg); err != nil {
			return nil, fmt.Errorf("plugin %q: %w", spec.Name, err)
		}
	}
	return reg, nil
}

// DecodeParams maps an untyped params map into a typed struct via a YAML round trip, so plugins can use ordinary
// structs with yaml tags.
func DecodeParams(params map[string]any, target any) error {
	data, err := yaml.Marshal(params)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}
