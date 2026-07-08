package executor

import (
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// MarkUnreachable excludes a host from all subsequent execution - both built-in phase steps (Hosts) and hook tasks
// (targetsForTask) - so once the deploy drops an unreachable host under on_unreachable: skip, nothing targets it again.
func (e *Executor) MarkUnreachable(host string) {
	e.unreachableMu.Lock()
	defer e.unreachableMu.Unlock()
	e.unreachable[host] = true
}

// filterExcluded drops hosts marked unreachable.
func (e *Executor) filterExcluded(hosts []ast.Host) []ast.Host {
	e.unreachableMu.Lock()
	defer e.unreachableMu.Unlock()
	if len(e.unreachable) == 0 {
		return hosts
	}
	out := make([]ast.Host, 0, len(hosts))
	for _, h := range hosts {
		if !e.unreachable[h.Address] {
			out = append(out, h)
		}
	}
	return out
}

// targetsForTask resolves the hosts a task runs on: by default deploy-enabled hosts filling its roles, narrowed by the
// global --roles/--host filters, then reduced to one host if the task is "once". deploy:false hosts are excluded so
// deploy hooks (e.g. restart) never touch an inventory-only host.
// Two task flags change the base set: all_hosts targets every host (deploy flag ignored) and non_deploy targets only
// the deploy:false hosts, all_hosts wins if both are set.
func (e *Executor) targetsForTask(task *ast.Task) []ast.Host {
	var hosts []ast.Host
	switch {
	case task.AllHosts:
		hosts = e.cfg.Hosts
	case task.NonDeploy:
		hosts = ast.FilterNonDeployable(e.cfg.Hosts)
	default:
		hosts = ast.FilterDeployable(e.cfg.Hosts)
	}
	hosts = ast.FilterByRoles(hosts, task.Roles)
	hosts = ast.FilterByRoles(hosts, e.roles)
	hosts = ast.FilterByAddresses(hosts, e.limit)
	hosts = e.filterExcluded(hosts)
	if task.Once && len(hosts) > 1 {
		hosts = ast.PickPrimary(hosts)
	}
	return hosts
}

// Targets converts deployfile hosts into runner targets, carrying each host's transport (SSH vs local).
// It is exported so the CLI's ad-hoc `run` builds its targets the same way task execution does.
func Targets(hosts []ast.Host) []runner.Target {
	targets := make([]runner.Target, len(hosts))
	for i, h := range hosts {
		targets[i] = runner.Target{Host: h.Address, Port: h.Port, User: h.User, IdentityFile: h.IdentityFile, Passphrase: h.IdentityFilePassphrase, Local: h.Local}
	}
	return targets
}

// taskTargets converts a task's hosts to runner targets, applying the task's strict_host_key override (if set) to each
// so the cluster dials those hosts with host-key verification toggled for this task only.
func (e *Executor) taskTargets(task *ast.Task, hosts []ast.Host) []runner.Target {
	targets := Targets(hosts)
	if task.StrictHostKey != nil {
		for i := range targets {
			targets[i].StrictHostKey = task.StrictHostKey
		}
	}
	return targets
}
