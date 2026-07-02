package cli

import (
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/transport/ssh"
)

// selectHosts picks the hosts an action targets: deploy-enabled hosts (the default), filtered by role and host.
// Empty role/host filters match everything, deploy:false hosts are inventory-only and never selected here.
func selectHosts(cfg *ast.DeployFile, roles, limit []string) []ast.Host {
	hosts := ast.FilterDeployable(cfg.Hosts)
	return ast.FilterByAddresses(ast.FilterByRoles(hosts, roles), limit)
}

// sshOptions derives connection options from the config.
// Host-key checking is strict unless ssh.strict_host_key is explicitly false.
func sshOptions(cfg *ast.DeployFile) ssh.Options {
	strict := cfg.SSH.StrictHostKey == nil || *cfg.SSH.StrictHostKey
	return ssh.Options{
		StrictHostKey:  strict,
		KnownHostsFile: cfg.SSH.KnownHostsFile,
		ForwardAgent:   cfg.SSH.ForwardAgent != nil && *cfg.SSH.ForwardAgent,
		ForwardKey:     cfg.SSH.ForwardKey,
	}
}
