package ast

// Merge layers the override config on top of the base and returns the result.
// Scalars and map entries from override win. Slices differ by field: hosts, plugins, env_files, and custom_phases
// concatenate (base first), while linked_files/linked_dirs replace wholesale (a non-empty override wins).
// Tasks and hooks are merged per key.
func Merge(base, override *DeployFile) *DeployFile {
	out := *base // shallow copy of base scalars

	if override.Version != "" {
		out.Version = override.Version
	}
	if override.ScriptsDir != "" {
		out.ScriptsDir = override.ScriptsDir
	}
	if override.OnUnreachable != "" {
		out.OnUnreachable = override.OnUnreachable
	}
	out.Plugins = append(append([]PluginSpec{}, base.Plugins...), override.Plugins...)
	out.App = mergeApp(base.App, override.App)
	// linked_files/linked_dirs replace wholesale (a non-empty override wins).
	// Copy rather than alias so the result never shares a backing array with base or override - matching how
	// servers/plugins/env_files are merged below.
	if len(override.LinkedFiles) > 0 {
		out.LinkedFiles = append([]string{}, override.LinkedFiles...)
	} else {
		out.LinkedFiles = append([]string{}, base.LinkedFiles...)
	}
	if len(override.LinkedDirs) > 0 {
		out.LinkedDirs = append([]string{}, override.LinkedDirs...)
	} else {
		out.LinkedDirs = append([]string{}, base.LinkedDirs...)
	}
	out.SSH = mergeSSH(base.SSH, override.SSH)
	out.Log = mergeLog(base.Log, override.Log)
	out.Vars = mergeMap(base.Vars, override.Vars)
	out.Envs = mergeMap(base.Envs, override.Envs)
	// Env files concatenate (shared first, then the stage's), so a stage adds to the base set; later entries override
	// earlier ones at load time.
	out.EnvFiles = append(append([]string{}, base.EnvFiles...), override.EnvFiles...)

	// Hosts come from the stage file; combine in case the base declares any.
	out.Hosts = append(append([]Host{}, base.Hosts...), override.Hosts...)
	// Custom phases concatenate (shared first, then the stage's), like hosts.
	out.CustomPhases = append(append([]CustomPhase{}, base.CustomPhases...), override.CustomPhases...)

	out.Tasks = mergeMap(base.Tasks, override.Tasks)
	out.Hooks = mergeHooks(base.Hooks, override.Hooks)
	return &out
}

func mergeLog(base, ov Log) Log {
	if ov.Level != "" {
		base.Level = ov.Level
	}
	if ov.Format != "" {
		base.Format = ov.Format
	}
	if ov.Output != "" {
		base.Output = ov.Output
	}
	if ov.Color != nil {
		base.Color = ov.Color
	}
	if ov.File != "" {
		base.File = ov.File
	}
	if ov.FileFormat != "" {
		base.FileFormat = ov.FileFormat
	}
	if ov.RawRemoteLog != nil {
		base.RawRemoteLog = ov.RawRemoteLog
	}
	return base
}

func mergeApp(base, ov App) App {
	if ov.Name != "" {
		base.Name = ov.Name
	}
	if ov.Repo != "" {
		base.Repo = ov.Repo
	}
	if ov.Branch != "" {
		base.Branch = ov.Branch
	}
	if ov.DeployTo != "" {
		base.DeployTo = ov.DeployTo
	}
	if ov.KeepReleases != 0 {
		base.KeepReleases = ov.KeepReleases
	}
	return base
}

func mergeSSH(base, ov SSH) SSH {
	if ov.User != "" {
		base.User = ov.User
	}
	if ov.Port != 0 {
		base.Port = ov.Port
	}
	if ov.IdentityFile != "" {
		base.IdentityFile = ov.IdentityFile
	}
	if ov.IdentityFilePassphrase != "" {
		base.IdentityFilePassphrase = ov.IdentityFilePassphrase
	}
	if ov.KnownHostsFile != "" {
		base.KnownHostsFile = ov.KnownHostsFile
	}
	if ov.StrictHostKey != nil {
		base.StrictHostKey = ov.StrictHostKey
	}
	if ov.AcceptNew != nil {
		base.AcceptNew = ov.AcceptNew
	}
	if ov.ForwardAgent != nil {
		base.ForwardAgent = ov.ForwardAgent
	}
	if ov.ForwardKey != "" {
		base.ForwardKey = ov.ForwardKey
	}
	// Pointer-wins: a stage replaces the base bastion wholesale, it cannot unset one.
	if ov.Bastion != nil {
		base.Bastion = ov.Bastion
	}
	base.Identities = mergeMap(base.Identities, ov.Identities)
	return base
}

// mergeMap combines two maps per key: a key in the override replaces the base's whole value (a task, a hook list, a
// var), not a field-level merge. Values are shared with base/override, so pointer values (*Task) must be treated as
// immutable after load - nothing mutates a Task in place.
func mergeMap[V any](base, ov map[string]V) map[string]V {
	if base == nil && ov == nil {
		return nil
	}
	out := make(map[string]V, len(base)+len(ov))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range ov {
		out[k] = v
	}
	return out
}

// mergeHooks merges the before/after maps per key: a stage that lists hooks for a phase/task replaces the base's whole
// list for that key (consistent with tasks and vars), it does not append to it.
func mergeHooks(base, ov Hooks) Hooks {
	return Hooks{
		Before: mergeMap(base.Before, ov.Before),
		After:  mergeMap(base.After, ov.After),
	}
}
