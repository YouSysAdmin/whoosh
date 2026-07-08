package ast

// FilterByRoles returns the hosts filling at least one of the given roles. An empty roles slice matches every host.
func FilterByRoles(hosts []Host, roles []string) []Host {
	if len(roles) == 0 {
		return hosts
	}
	var out []Host
	for _, h := range hosts {
		for _, r := range roles {
			if h.HasRole(r) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// FilterDeployable returns the hosts the release lifecycle should target - those with deploy enabled (the default).
// Hosts with deploy:false stay in inventory (listed by `config` and the `deploy:hosts` command) but are skipped for
// execution.
func FilterDeployable(hosts []Host) []Host {
	var out []Host
	for _, h := range hosts {
		if h.DeployEnabled() {
			out = append(out, h)
		}
	}
	return out
}

// FilterNonDeployable returns the hosts with deploy disabled (deploy:false) - the complement of FilterDeployable.
// These are inventory-only hosts (e.g.
// ASG instances booted from a baked AMI) that the release lifecycle skips, a task with non_deploy:true targets exactly
// this set.
func FilterNonDeployable(hosts []Host) []Host {
	var out []Host
	for _, h := range hosts {
		if !h.DeployEnabled() {
			out = append(out, h)
		}
	}
	return out
}

// PickPrimary reduces hosts to the single host preferred for one-host work (`once:` tasks, the deploy lock): the
// first host marked primary, or the first host when none is marked. An empty input returns nil.
func PickPrimary(hosts []Host) []Host {
	if len(hosts) == 0 {
		return nil
	}
	for _, h := range hosts {
		if h.Primary {
			return []Host{h}
		}
	}
	return hosts[:1]
}

// FilterByAddresses returns the hosts whose address is in addrs. An empty addrs slice matches every host.
func FilterByAddresses(hosts []Host, addrs []string) []Host {
	if len(addrs) == 0 {
		return hosts
	}
	var out []Host
	for _, h := range hosts {
		for _, a := range addrs {
			if h.Address == a {
				out = append(out, h)
				break
			}
		}
	}
	return out
}
