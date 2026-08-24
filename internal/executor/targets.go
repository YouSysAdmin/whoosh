package executor

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	werrors "github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// MarkUnreachable excludes a host from all subsequent execution - both built-in phase steps (Hosts) and hook tasks
// (targetsForTask) - so once the deploy drops an unreachable host under on_unreachable: skip, nothing targets it again.
func (e *Executor) MarkUnreachable(host string) {
	e.unreachableMu.Lock()
	defer e.unreachableMu.Unlock()
	e.unreachable[host] = true
}

// UnreachableHosts returns the hosts dropped so far (sorted), for the deploy's final skipped-hosts report.
func (e *Executor) UnreachableHosts() []string {
	e.unreachableMu.Lock()
	defer e.unreachableMu.Unlock()
	hosts := make([]string, 0, len(e.unreachable))
	for h := range e.unreachable {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	return hosts
}

// SetUnreachablePolicy installs the deploy lifecycle's on_unreachable policy for task runs, so hook and custom-phase
// tasks apply the same host-drop rules as the built-in phase steps. required holds the hosts whose loss is always
// fatal (explicitly required hosts plus the lock-holding primary). Unset (the default) means abort on any failure.
func (e *Executor) SetUnreachablePolicy(policy string, required map[string]bool) {
	e.unreachablePolicy = policy
	e.requiredHosts = required
}

// skipUnreachable reports whether unreachable hosts are dropped rather than fatal.
func (e *Executor) skipUnreachable() bool { return e.unreachablePolicy == ast.OnUnreachableSkip }

// applyUnreachablePolicy applies on_unreachable to one task step's per-host results and returns the hosts still live.
// Under skip, an unreachable non-required host is dropped (marked, so nothing targets it again). Everything else is
// fatal: a command that ran and failed, a required or primary host lost, a context cancellation (an operator Ctrl-C or
// a cancelled sibling says nothing about the host), or any failure under the default abort policy.
func (e *Executor) applyUnreachablePolicy(task string, hosts []ast.Host, results []runner.Result) ([]ast.Host, error) {
	if !e.skipUnreachable() {
		return hosts, firstError(results)
	}
	return SkipUnreachable(hosts, results, func(h string) bool { return e.requiredHosts[h] }, func(host string, err error) {
		slog.Warn("host unreachable, skipping", "task", task, "host", host, "error", err)
		e.MarkUnreachable(host)
	})
}

// SkipUnreachable is the on_unreachable: skip verdict for one step's per-host results, shared by task execution and
// the deploy lifecycle so the policy cannot drift between them. An unreachable, non-required host is handed to drop
// (the caller warns and marks it) and removed from hosts. Everything else is fatal: a command that ran and failed, a
// required host lost, or a context cancellation - and like firstError, a real failure is preferred over a cancelled
// bystander when picking the returned error.
func SkipUnreachable(hosts []ast.Host, results []runner.Result, required func(string) bool, drop func(host string, err error)) ([]ast.Host, error) {
	var canceled error
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if werrors.Is(r.Err, context.Canceled) || werrors.Is(r.Err, context.DeadlineExceeded) {
			if canceled == nil {
				canceled = fmt.Errorf("%s: %w", r.Host, r.Err)
			}
			continue
		}
		if !werrors.IsUnreachable(r.Err) {
			return hosts, fmt.Errorf("%s: %w", r.Host, r.Err)
		}
		if required(r.Host) {
			return hosts, fmt.Errorf("required host %s unreachable: %w", r.Host, r.Err)
		}
	}
	if canceled != nil {
		return hosts, canceled
	}
	dropped := map[string]bool{}
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		drop(r.Host, r.Err)
		dropped[r.Host] = true
	}
	if len(dropped) == 0 {
		return hosts, nil
	}
	live := make([]ast.Host, 0, len(hosts))
	for _, h := range hosts {
		if !dropped[h.Address] {
			live = append(live, h)
		}
	}
	return live, nil
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
