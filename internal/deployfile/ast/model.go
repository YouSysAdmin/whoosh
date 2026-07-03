// Package ast is the Deployfile data model: the structs the YAML unmarshals into, plus the pure transforms over them
// (Merge, ApplyDefaults, Validate, the version gate, AsMap, and the host filters).
// It does no file I/O - the deployfile package layers loading, includes, and rendering on top of this.
package ast

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// !!! THIS FILE IS A BASIS, PLEASE GENEROUSLY ADD COMMENTS TO THE TYPES !!!

// ParseLinkedPath splits a linked_files/linked_dirs entry into the path under shared/ (source) and the path the symlink
// is created at in the release (dest). A "source:dest" entry rewrites the destination - e.g.
// "config/database.yml:config/prod.yml" links shared/config/database.yml to <release>/config/prod.yml; a bare "path"
// uses the same location on both sides (dest == source). The split is on the first colon, so paths must not contain one.
func ParseLinkedPath(entry string) (source, dest string) {
	if src, dst, ok := strings.Cut(entry, ":"); ok {
		return src, dst
	}
	return entry, entry
}

// StringList is a list of strings that also accepts a single scalar in YAML, so both `include: shared/x.yml` and
// `include: [a, b]` parse to the same value.
type StringList []string

// UnmarshalYAML accepts either a scalar string or a sequence of strings.
func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var one string
		if err := value.Decode(&one); err != nil {
			return err
		}
		*s = StringList{one}
	case yaml.SequenceNode:
		var many []string
		if err := value.Decode(&many); err != nil {
			return err
		}
		*s = StringList(many)
	default:
		return fmt.Errorf("expected a string or a list of strings")
	}
	return nil
}

// DeployFile is the deployment configuration.
// The same shape is used for both the shared Deployfile and a per-stage file, the two are deserialized independently
// and combined with Merge. After Load it represents the fully resolved config for one stage.
type DeployFile struct {
	Version string `yaml:"version,omitempty"` // Marker of Whooshfile, Deployfile schema version
	// Include lists other config files to merge underneath this one (require_relative style: paths resolve against this
	// file's directory). Includes are layered in listed order with this file winning, then nested recursively.
	// Resolved at load time; never present on the merged result.
	Include     StringList     `yaml:"include,omitempty"`
	App         App            `yaml:"app,omitempty"`          // The application being deployed: name, repo, branch, deploy_to, keep_releases
	LinkedFiles []string       `yaml:"linked_files,omitempty"` // Files symlinked from shared/ into every release; "source:dest" rewrites the release-side path
	LinkedDirs  []string       `yaml:"linked_dirs,omitempty"`  // Dirs symlinked from shared/ into every release; "source:dest" rewrites the release-side path
	Vars        map[string]any `yaml:"vars,omitempty"`         // Template/env values
	// Envs is a default environment exported for every task command and script (merged with a task's own `envs`, which
	// wins). Values are shell-expanded, so e.g. PATH can reference $HOME/$PATH - useful for rbenv/nvm shims.
	Envs map[string]string `yaml:"envs,omitempty"`
	// EnvFiles are dotenv (.env) files loaded into every task's environment as a base layer beneath the global and
	// per-task `envs` (which override).
	// Paths resolve against the config-file dir; later entries win (stage-file entries are appended after the shared
	// ones), a missing file is skipped, and the loaded values are runtime-only - never emitted by `config`/{{.config}}.
	EnvFiles []string `yaml:"env_files,omitempty"`
	SSH      SSH      `yaml:"ssh,omitempty"`   // SSH config
	Hosts    []Host   `yaml:"hosts,omitempty"` // List of hosts for an app deploying

	Tasks map[string]*Task `yaml:"tasks,omitempty"` // List of deployment tasks
	Hooks Hooks            `yaml:"hooks,omitempty"` // List of deployment hooks
	// ScriptsDir overrides the directory that task `scripts` reference by name.
	// Relative paths are resolved against the Deployfile's directory; the default is deploy/scripts.
	ScriptsDir string `yaml:"scripts_dir,omitempty"`
	// OnUnreachable is the policy for a host that becomes unreachable during a deploy: "abort" (default - fail the whole
	// deploy) or "skip" (drop the host, finish on the survivors, and exit non-zero).
	// It applies to connectivity failures only; a command that runs and exits non-zero always aborts.
	OnUnreachable string `yaml:"on_unreachable,omitempty" jsonschema:"enum=abort,enum=skip"`
	// Log configures whoosh's own logging (level/format/output/color and an optional log file).
	// Command-line --log-* flags override these when set.
	Log Log `yaml:"log,omitempty"`
	// Plugins is the ordered list of plugins to load with their params.
	// Each one validates its params on load and registers startup hooks and/or actions.
	Plugins []PluginSpec `yaml:"plugins,omitempty"`
	// CustomPhases inserts named phases into the deploy lifecycle, each anchored before or after a built-in phase.
	// A custom phase runs its Task (if set) and is itself a before/after hook anchor.
	// Concatenated across base+stage like hosts/plugins; validated when the deploy starts.
	CustomPhases []CustomPhase `yaml:"custom_phases,omitempty"`

	// Stage is the resolved stage name. It is not read from YAML.
	Stage string `yaml:"-"`
	// Dir is the directory containing the Deployfile. It is not read from YAML.
	Dir string `yaml:"-"`
	// SkippedPlugins names plugins filtered out for this stage (by Only/Except).
	// Action tasks whose action namespace matches one of these are skipped instead of failing.
	// Populated at load; not read from YAML.
	SkippedPlugins []string `yaml:"-"`
	// Imports holds values a plugin startup hook injected into the template context, keyed namespace -> key -> value
	// (e.g. SSM parameters under "ssm"). Exposed to templates as {{ .<ns>.<key> }} and to commands as $<NS>_<KEY>.
	// Runtime-only (never read from or written to YAML), so the values are not emitted by the `config` command or surfaced
	// under {{.config}} - keep secrets here.
	Imports map[string]map[string]string `yaml:"-"`
	// EnvFileValues holds the KEY=value pairs merged from EnvFiles at load time.
	// Runtime-only (yaml:"-") so dotenv contents never reach `config`/{{.config}}; the executor applies them to each
	// task's environment.
	EnvFileValues map[string]string `yaml:"-"`
	// HookFuncsBefore/After hold plugins-registered Go functions to run before/after a deploy phase, keyed by phase name.
	// Unlike the task hooks (Hooks), these run the plugin's own code with the deploy's console writer - for emitting
	// operator-side output (e.g. print-hosts-table).
	// Runtime-only (yaml:"-"), and run only during the deploy lifecycle, so they never fire for config/hosts/run.
	HookFuncsBefore map[string][]HookFunc `yaml:"-"`
	HookFuncsAfter  map[string][]HookFunc `yaml:"-"`
}

// Log configures whoosh's own logging.
// An empty field falls back to the matching --log-* flag value, a flag the operator explicitly sets always wins.
type Log struct {
	Level      string `yaml:"level,omitempty" jsonschema:"enum=debug,enum=info,enum=warn,enum=error"` // debug, info, warn, error
	Format     string `yaml:"format,omitempty" jsonschema:"enum=text,enum=json"`                      // text or json
	Output     string `yaml:"output,omitempty"`                                                       // stdout, stderr, or a file path
	Color      *bool  `yaml:"color,omitempty"`                                                        // colorize text logs (terminal only)
	File       string `yaml:"file,omitempty"`                                                         // also write a deploy log to this file
	FileFormat string `yaml:"file_format,omitempty" jsonschema:"enum=text,enum=json"`                 // text or json for File
	// RawRemoteLog controls how command output (and the echoed commands) is shown. When set it wins: true streams raw to
	// the console (host-prefixed, as it arrives), false emits each line through the logger as a structured record (with
	// the host and running task), so command output joins the JSON log stream and can be shipped to a collector.
	// When unset, the default follows the log format - text streams raw, json routes through slog - so a json-format run stays
	// a single valid JSON stream instead of interleaving raw host lines.
	RawRemoteLog *bool `yaml:"raw_remote_log,omitempty"`
}

// RawOutput reports whether command output (and the echoed commands) is streamed raw to the console, host-prefixed,
// rather than routed through slog as structured records.
// An explicit raw_remote_log wins; when unset the default follows the log format - text streams raw, json routes
// through slog, so a json-format run isn't corrupted by interleaved raw host lines.
// Format carries the effective log format (the CLI materializes --log-format into it).
func (l Log) RawOutput() bool {
	if l.RawRemoteLog != nil {
		return *l.RawRemoteLog
	}
	return !strings.EqualFold(l.Format, "json")
}

// AddImport records a namespaced value for the template context (see Imports).
// It lazily allocates the maps, the last write for a (namespace, key) wins.
func (c *DeployFile) AddImport(namespace, key, value string) {
	if c.Imports == nil {
		c.Imports = map[string]map[string]string{}
	}
	if c.Imports[namespace] == nil {
		c.Imports[namespace] = map[string]string{}
	}
	c.Imports[namespace][key] = value
}

// AddTask registers a task under name, lazily allocating the map.
// Meant for a plugin startup hook contributing a task that a hook or custom phase then runs.
func (c *DeployFile) AddTask(name string, t *Task) {
	if c.Tasks == nil {
		c.Tasks = map[string]*Task{}
	}
	c.Tasks[name] = t
}

// AddHookBefore appends task names to run before the given key (a phase - built-in or custom - or a task name), lazily
// allocating the map.
func (c *DeployFile) AddHookBefore(key string, tasks ...string) {
	if c.Hooks.Before == nil {
		c.Hooks.Before = map[string][]string{}
	}
	c.Hooks.Before[key] = append(c.Hooks.Before[key], tasks...)
}

// AddHookAfter appends task names to run after the given key (a phase - built-in or custom - or a task name), lazily
// allocating the map.
func (c *DeployFile) AddHookAfter(key string, tasks ...string) {
	if c.Hooks.After == nil {
		c.Hooks.After = map[string][]string{}
	}
	c.Hooks.After[key] = append(c.Hooks.After[key], tasks...)
}

// HookFunc is a plugin-registered function run before/after a deploy phase, with the deploy's console writer (the same
// stream command output goes to).
// It is the direct-output counterpart to a task hook: the plugins runs its own Go code at the phase instead of naming a
// task. Registered via AddHookFuncBefore/After, a returned error aborts the deploy like a failing task hook.
type HookFunc func(ctx context.Context, out io.Writer) error

// AddHookFuncBefore registers fn to run before the given phase (see HookFunc), lazily allocating the map.
func (c *DeployFile) AddHookFuncBefore(phase string, fn HookFunc) {
	if c.HookFuncsBefore == nil {
		c.HookFuncsBefore = map[string][]HookFunc{}
	}
	c.HookFuncsBefore[phase] = append(c.HookFuncsBefore[phase], fn)
}

// AddHookFuncAfter registers fn to run after the given phase (see HookFunc), lazily allocating the map.
func (c *DeployFile) AddHookFuncAfter(phase string, fn HookFunc) {
	if c.HookFuncsAfter == nil {
		c.HookFuncsAfter = map[string][]HookFunc{}
	}
	c.HookFuncsAfter[phase] = append(c.HookFuncsAfter[phase], fn)
}

// AddPhase appends a custom phase to the lifecycle (see CustomPhase).
func (c *DeployFile) AddPhase(p CustomPhase) {
	c.CustomPhases = append(c.CustomPhases, p)
}

// PluginSpec declares one plugin to load and how to configure it.
// Params are the plugin's global params (e.g. shared AWS credentials), Actions configures and/or enables a plugin's
// individual features (e.g. aws:ec2:inventory with its tags).
// Only/Except gate which stages the plugin is active in - Only lists the stages it runs in (empty = all), Except lists
// stages to skip, a stage in Except is always skipped.
// When a plugin is inactive for a stage, action tasks bound to it are skipped rather than failing.
type PluginSpec struct {
	Name string `yaml:"name"` // The plugin's registered name (e.g. "aws")
	// Enabled is a coarse on/off switch, independent of stage.
	// Omitted (nil) or true loads the plugin; false turns it off entirely - its startup hooks and actions are never
	// registered and action tasks bound to it are skipped (logged) rather than failing, exactly like an
	// Only/Except-inactive plugins.
	Enabled *bool              `yaml:"enabled,omitempty"`
	Only    []string           `yaml:"only,omitempty"`    // Stages this plugins is active in (empty = all)
	Except  []string           `yaml:"except,omitempty"`  // Stages to skip (wins over Only)
	Params  map[string]any     `yaml:"params,omitempty"`  // The plugin's global params
	Actions []PluginActionSpec `yaml:"actions,omitempty"` // Per-feature configuration and/or enablement
}

// PluginActionSpec configures one of a plugin's named features (its global name, e.g. aws:ec2:inventory) with
// feature-specific params, layered on the plugin's global Params.
type PluginActionSpec struct {
	Name   string         `yaml:"name"`             // The feature's global name (e.g. aws:ec2:inventory)
	Params map[string]any `yaml:"params,omitempty"` // Feature-specific params, layered on the plugin's global Params
}

// ActiveForStage reports whether the plugin is active for the given stage under its Only/Except filters (Except wins,
// both empty = active everywhere).
func (p PluginSpec) ActiveForStage(stage string) bool {
	return stageActive(stage, p.Only, p.Except)
}

// IsEnabled reports whether the plugins should load.
// A nil Enabled (the field omitted, the default) means enabled, only an explicit enabled: false disables it.
func (p PluginSpec) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// stageActive is the shared Only/Except stage gate used by both PluginSpec and Task: a stage listed in except is never
// active (except wins), otherwise an empty only matches every stage, and a non-empty only matches only its members.
func stageActive(stage string, only, except []string) bool {
	for _, s := range except {
		if s == stage {
			return false
		}
	}
	if len(only) == 0 {
		return true
	}
	for _, s := range only {
		if s == stage {
			return true
		}
	}
	return false
}

// App describes the application being deployed and where it lives on the targets.
type App struct {
	Name     string `yaml:"name,omitempty"`      // Application name; used in logs and as {{.app_name}} / $APP_NAME
	Repo     string `yaml:"repo,omitempty"`      // Git remote to deploy from (required to deploy)
	Branch   string `yaml:"branch,omitempty"`    // Branch to deploy (default: master)
	DeployTo string `yaml:"deploy_to,omitempty"` // Deploy root on each host; holds releases/, shared/, repo/, current (required)
	// KeepReleases is how many releases to keep on each host; older ones are pruned at deploy:finishing (default 5).
	// Exposed to templates/scripts as {{.keep_releases}} / $KEEP_RELEASES.
	KeepReleases int `yaml:"keep_releases,omitempty"`
}

// SSH holds connection defaults applied to every host unless the host overrides them.
type SSH struct {
	User           string `yaml:"user,omitempty"`             // SSH login user
	Port           int    `yaml:"port,omitempty"`             // SSH port (default: 22)
	IdentityFile   string `yaml:"identity_file,omitempty"`    // Private key file used to authenticate
	KnownHostsFile string `yaml:"known_hosts_file,omitempty"` // known_hosts file for host-key verification
	// StrictHostKey toggles host-key verification. nil means "use the default" (true).
	StrictHostKey *bool `yaml:"strict_host_key,omitempty"`
	// AcceptNew, with strict host-key checking, trusts a host seen for the first time (OpenSSH accept-new): its key is
	// appended to the known_hosts file (created when missing) and the connection proceeds. A changed key still fails.
	// nil means the default (true); set false to require every host key to be present in known_hosts already.
	AcceptNew *bool `yaml:"accept_new,omitempty"`
	// ForwardAgent forwards the operator's local ssh-agent to each host, so commands there (notably git) authenticate to
	// remotes with the operator's keys. nil means the default (false - the host uses its own keys); a stage can
	// explicitly set false to disable forwarding the base enabled.
	ForwardAgent *bool `yaml:"forward_agent,omitempty"`
	// ForwardKey forwards only the key at this path, presented in-memory over the agent protocol (never written to the
	// host). Takes precedence over ForwardAgent when both are set.
	ForwardKey string `yaml:"forward_key,omitempty"`
}

// Host is a single deployment target, tagged with the roles it fills.
// When Local is set, commands run on the operator's machine via the local shell and the SSH fields are ignored - this
// is "local execution mode".
type Host struct {
	Address      string   `yaml:"address,omitempty" json:"address,omitempty"`             // Host address (IP or DNS name)
	Roles        []string `yaml:"roles,omitempty" json:"roles,omitempty"`                 // Roles this host fills; tasks target hosts by role
	User         string   `yaml:"user,omitempty" json:"user,omitempty"`                   // Overrides ssh.user for this host
	Port         int      `yaml:"port,omitempty" json:"port,omitempty"`                   // Overrides ssh.port for this host
	IdentityFile string   `yaml:"identity_file,omitempty" json:"identity_file,omitempty"` // Overrides ssh.identity_file for this host
	Local        bool     `yaml:"local,omitempty" json:"local,omitempty"`                 // Run on the operator's machine via the local shell (SSH fields ignored)
	// Deploy gates whether the release lifecycle, tasks, hooks, and ad-hoc run target this host. nil means the default
	// (true).
	// Set false to keep a host in inventory - listed by `config` and the `deploy:hosts` command - without deploying the
	// app to it.
	Deploy *bool `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	// Required, when true, makes this host's unreachability fatal even under on_unreachable: skip - so a critical host is
	// never silently dropped. nil means the default (false).
	Required *bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Source records where the host came from: HostSourceConfig ("config") for one declared in the Deployfile, or the
	// name of the plugin feature that discovered it (e.g. "aws:ec2:inventory"). Runtime-only (yaml:"-"); ApplyDefaults
	// stamps "config" on any host without a source, and an inventory plugin sets its own source when it appends a host.
	// Shown in the deploy:hosts table.
	Source string `yaml:"-" json:"source,omitempty"`
}

// Task is a named unit of work. Commands run on the hosts matching Roles (or locally when Local is set).
// Cmds run first, then Scripts, both in listed order.
type Task struct {
	Desc    string            `yaml:"desc,omitempty"`    // Human-readable description shown in the CLI listing
	Cmds    []string          `yaml:"cmds,omitempty"`    // Shell commands run in order on the targets (Go-templated)
	Scripts []Script          `yaml:"scripts,omitempty"` // Shell scripts run after cmds, in order
	Deps    []string          `yaml:"deps,omitempty"`    // Other task names to run before this one
	Dir     string            `yaml:"dir,omitempty"`     // Working dir for cmds/scripts (templated; default release dir, cwd for local)
	Envs    map[string]string `yaml:"envs,omitempty"`    // Extra environment, layered over global envs (task wins)
	Roles   []string          `yaml:"roles,omitempty"`   // Restrict to hosts filling these roles (default: all deployable hosts)
	Local   bool              `yaml:"local,omitempty"`   // Run on the operator's machine instead of the target hosts
	Once    bool              `yaml:"once,omitempty"`    // When multiple hosts match, run on a single host only
	Silent  bool              `yaml:"silent,omitempty"`  // Suppress the task's start announcement in the log
	// SilentOutput hides the task's command output (and the echoed commands): it is buffered instead of shown, discarded
	// on success, and flushed to the console (redacted) only if the task fails - so a noisy task stays quiet without
	// hiding failures. Orthogonal to Silent (which suppresses the start announcement); the task is still announced.
	// Ignored under --dry-run. With continue_on_error the run "succeeds", so the buffer is discarded (per-host failures
	// still surface as warnings).
	SilentOutput    bool `yaml:"silent_output,omitempty"`
	ContinueOnError bool `yaml:"continue_on_error,omitempty"` // Don't abort on a non-zero exit; log each failed host and continue
	// NonDeploy targets the inventory's *non-deployable* hosts (deploy:false) instead of the deployable ones - the inverse
	// of the normal task targeting. Roles still narrow within that set.
	// Intended for a task run as its own invocation after a deploy (e.g. a healthcheck on freshly refreshed ASG hosts): a
	// new run re-fetches the inventory, so those hosts are present.
	// Within the deploy lifecycle the inventory is fixed at startup, so a non_deploy hook would see the pre-deploy host
	// list.
	NonDeploy bool `yaml:"non_deploy,omitempty"`
	// AllHosts targets every host in the inventory, ignoring the per-server deploy flag (both deploy:true and
	// deploy:false) - for a task that should hit the whole fleet (e.g. collecting disk usage).
	// Roles/limit still narrow the set. Takes precedence over NonDeploy.
	// Like NonDeploy, it operates on the inventory captured at startup (see the note there).
	AllHosts bool `yaml:"all_hosts,omitempty"`
	// StrictHostKey overrides the stage's ssh.strict_host_key for this task's SSH connections. nil inherits the stage
	// setting; false skips known_hosts verification (e.g. for ASG instances launched from one AMI that share a key and
	// rotate IPs, where the key is legitimately "unknown"); true forces it.
	StrictHostKey *bool `yaml:"strict_host_key,omitempty"`
	// Hidden omits the task from the CLI listing (`whoosh <stage> --help`).
	// The task is still runnable directly by name and still usable as a dep or hook - useful for helper tasks (a `setup`
	// dep, a hook-only `restore-manifest`) that shouldn't clutter the command list.
	Hidden bool `yaml:"hidden,omitempty"`

	// Only/Except gate which stages the task is active in, mirroring PluginSpec - Only lists the stages it runs in (empty
	// = all), Except lists stages to skip; a stage in Except is always skipped (Except wins).
	// When a task is inactive for the current stage it is skipped (logged) rather than run - whether it is invoked
	// directly, as a dependency, or from a hook - and it is omitted from the stage's CLI listing.
	// So a hook can name a task that only some stages run.
	Only   []string `yaml:"only,omitempty"`
	Except []string `yaml:"except,omitempty"`

	// Replace, when set to a phase name, makes this task run in place of that phase's built-in command (its before/after
	// hooks still run).
	// Only `deploy:rollback` is replaceable - e.g. an `aws:ec2:asg:rollback` action task can take over `whoosh <stage>
	// deploy:rollback` instead of the default current-symlink swap.
	Replace string `yaml:"replace,omitempty"`

	// Output, when set, captures the task's combined stdout and stores the parsed result in run-scoped state under the
	// task's name, for later tasks to read as {{ .tasks.<name> }}.
	// One of "json" (parsed to a map/list/scalar), "text" (trimmed string), or "lines" ([]string).
	// The task runs on a single target.
	Output string `yaml:"output,omitempty" jsonschema:"enum=json,enum=text,enum=lines"`

	// Action/With define an action task: it invokes a plugin-registered action by its global name (e.g. action:
	// aws:ec2:asg:refresh), operator-side. Mutually exclusive with cmds/scripts.
	Action string         `yaml:"action,omitempty"`
	With   map[string]any `yaml:"with,omitempty"`
}

// ActiveForStage reports whether the task is active for the given stage under its Only/Except filters (Except wins,
// both empty = active everywhere), the same gate plugins use.
func (t *Task) ActiveForStage(stage string) bool {
	return stageActive(stage, t.Only, t.Except)
}

// Script runs a shell script on the task's targets.
// Provide exactly one of Path (a file referenced by name from scripts_dir, or an absolute path) or Script (inline
// content, which is Go-templated like cmds).
// The script content is read on the operator's machine and streamed to Interpreter (default /bin/sh) on the target via
// stdin, so no upload is needed and SSH/local modes behave the same.
type Script struct {
	Name        string `yaml:"name,omitempty"`        // label; defaults to the file's base name
	Path        string `yaml:"path,omitempty"`        // script file (by name or absolute path)
	Script      string `yaml:"script,omitempty"`      // inline script content
	Interpreter string `yaml:"interpreter,omitempty"` // e.g. /bin/bash; default /bin/sh
	// Template renders a file script as a Go template before running it.
	// Files whose path ends in .tmpl are templated automatically. Inline scripts are always templated.
	Template bool `yaml:"template,omitempty"`
}

// Hooks maps a key to the task names run before or after it. The key is either a deploy phase name (e.g.
// "deploy:publishing"), fired by the deploy lifecycle, or a task name (e.g. "restart_sidekiq"), fired around every
// invocation of that task - directly, as a dependency, or as another phase/task hook - so a task can be wired to run
// before/after another without binding either to a phase.
type Hooks struct {
	Before map[string][]string `yaml:"before,omitempty"` // Phase or task name -> task names to run before it
	After  map[string][]string `yaml:"after,omitempty"`  // Phase or task name -> task names to run after it
}

// CustomPhase is a user/plugin-defined phase spliced into the deploy lifecycle, anchored relative to a built-in phase.
// Set exactly one of Before/After to a built-in phase name, Task (optional) is a task run at that point - when empty
// the phase is a pure hook anchor. Like a built-in phase, before/after hooks keyed by Name fire around it.
type CustomPhase struct {
	Name   string `yaml:"name"`             // The custom phase's name (also the hook key)
	Before string `yaml:"before,omitempty"` // Built-in phase to anchor before (set exactly one of before/after)
	After  string `yaml:"after,omitempty"`  // Built-in phase to anchor after
	Task   string `yaml:"task,omitempty"`   // Task to run at this phase; empty = pure hook anchor
}

// HasRole reports whether the host fills the given role.
func (h Host) HasRole(role string) bool {
	for _, r := range h.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// DeployEnabled reports whether the release lifecycle/tasks should target this host.
// It defaults to true when Deploy is unset.
func (h Host) DeployEnabled() bool { return h.Deploy == nil || *h.Deploy }

// IsRequired reports whether this host's unreachability is fatal regardless of the on_unreachable policy.
// It defaults to false when Required is unset.
func (h Host) IsRequired() bool { return h.Required != nil && *h.Required }
