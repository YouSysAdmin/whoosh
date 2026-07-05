package ast

import (
	"maps"
	"slices"

	"github.com/yousysadmin/whoosh/internal/errors"
)

// Validate checks the resolved config for the fields a deploy needs.
// Every failure is an errors.ConfigError (or the version gate's errors.VersionError), so the CLI exits with the config
// exit code.
func (c *DeployFile) Validate() error {
	if err := checkVersion(c.Version); err != nil {
		return err
	}
	if c.App.Name == "" {
		return errors.Config("app.name is required")
	}
	if c.App.DeployTo == "" {
		return errors.Config("app.deploy_to is required")
	}
	// The cleanup script removes count-keep_releases releases, so a negative value would select every release
	// (including the live one) for deletion. 0 means "use the default".
	if c.App.KeepReleases < 0 {
		return errors.Config("app.keep_releases must not be negative (got %d)", c.App.KeepReleases)
	}
	switch c.OnUnreachable {
	case "", OnUnreachableAbort, OnUnreachableSkip:
	default:
		return errors.Config("on_unreachable %q is invalid (want %q or %q)", c.OnUnreachable, OnUnreachableAbort, OnUnreachableSkip)
	}
	if c.SSH.IdentityFilePassphrase != "" && c.SSH.IdentityFile == "" {
		return errors.Config("ssh.identity_file_passphrase requires ssh.identity_file (identities entries have their own passphrase)")
	}
	for i, h := range c.Hosts {
		if h.Address == "" && !h.Local {
			return errors.Config("hosts[%d].address is required (or set local: true)", i)
		}
		if h.IdentityFilePassphrase != "" && h.IdentityFile == "" && !h.Local {
			return errors.Config("hosts[%d].identity_file_passphrase set but no identity_file to decrypt", i)
		}
	}
	for i, f := range c.LinkedFiles {
		if src, dst := ParseLinkedPath(f); src == "" || dst == "" {
			return errors.Config("linked_files[%d] %q: source and destination must both be non-empty (use \"source:dest\", no spaces)", i, f)
		}
	}
	for i, d := range c.LinkedDirs {
		if src, dst := ParseLinkedPath(d); src == "" || dst == "" {
			return errors.Config("linked_dirs[%d] %q: source and destination must both be non-empty (use \"source:dest\", no spaces)", i, d)
		}
	}
	for name, t := range c.Tasks {
		if t == nil {
			continue
		}
		for i, s := range t.Scripts {
			hasPath := s.Path != ""
			hasInline := s.Script != ""
			if hasPath == hasInline {
				return errors.Config("task %q scripts[%d]: set exactly one of 'path' or 'script'", name, i)
			}
		}
		if t.Action != "" && (len(t.Cmds) > 0 || len(t.Scripts) > 0) {
			return errors.Config("task %q: 'action' cannot be combined with cmds/scripts", name)
		}
		switch t.Output {
		case "", "json", "text", "lines":
		default:
			return errors.Config("task %q: invalid output %q (want json, text, or lines)", name, t.Output)
		}
		if t.Output != "" && t.Action != "" {
			return errors.Config("task %q: 'output' cannot be combined with 'action'", name)
		}
	}
	for i, spec := range c.Plugins {
		if spec.Name == "" {
			return errors.Config("plugins[%d]: 'name' is required", i)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.SSH.Identities)) {
		id := c.SSH.Identities[name]
		hasPath := id.Path != ""
		hasContent := id.Content != ""
		if hasPath == hasContent {
			return errors.Config("ssh.identities.%s: set exactly one of 'path' or 'content'", name)
		}
		if id.Recursive && !hasPath {
			return errors.Config("ssh.identities.%s: 'recursive' requires 'path'", name)
		}
	}
	return nil
}
