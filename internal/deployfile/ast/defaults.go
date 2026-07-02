package ast

// Defaults applied when a value is omitted.
const (
	DefaultKeepReleases = 5
	DefaultBranch       = "master"
	DefaultSSHPort      = 22
)

// on_unreachable policies: what a deploy does when a host becomes unreachable.
const (
	OnUnreachableAbort = "abort" // fail the whole deploy (default)
	OnUnreachableSkip  = "skip"  // drop the host and finish on the survivors
)

// HostSourceConfig is the Host.Source value for a host declared in the Deployfile,
// as opposed to one an inventory plugin discovered (which sets its own source).
const HostSourceConfig = "config"

// ApplyDefaults fills in omitted values (keep_releases, branch, per-host SSH settings, local host labels).
// It is idempotent, so it can be re-run after dynamic inventory appends hosts.
func (c *DeployFile) ApplyDefaults() {
	// Sets default num of KeepReleases
	if c.App.KeepReleases == 0 {
		c.App.KeepReleases = DefaultKeepReleases
	}

	// Sets default git branch
	if c.App.Branch == "" {
		c.App.Branch = DefaultBranch
	}

	// Sets default SSH port
	if c.SSH.Port == 0 {
		c.SSH.Port = DefaultSSHPort
	}

	// Sets default list of hosts
	for i := range c.Hosts {
		// Stamp the host's origin if it doesn't have one. A host from the Deployfile
		// gets "config"; an inventory plugin sets its own source before this re-runs,
		// so its hosts keep it. Done for every host, local ones included.
		if c.Hosts[i].Source == "" {
			c.Hosts[i].Source = HostSourceConfig
		}
		if c.Hosts[i].Local {
			// Local targets without SSH settings, just give them a label.
			if c.Hosts[i].Address == "" {
				c.Hosts[i].Address = "localhost"
			}
			continue
		}
		if c.Hosts[i].Port == 0 {
			c.Hosts[i].Port = c.SSH.Port
		}
		if c.Hosts[i].User == "" {
			c.Hosts[i].User = c.SSH.User
		}
		if c.Hosts[i].IdentityFile == "" {
			c.Hosts[i].IdentityFile = c.SSH.IdentityFile
		}
	}
}
