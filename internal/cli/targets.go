package cli

import (
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/errors"
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
// When ssh.identity_file or ssh.identities is set, the builtin in-memory agent is built here - once per command, the
// keyring is shared by every connection - and the system ssh-agent is not consulted.
// When ssh.bastion is set, one shared jump-host connection (opened lazily by the first dial) tunnels every host
// connection. The cluster closes it on teardown.
func sshOptions(cfg *ast.DeployFile) (ssh.Options, error) {
	strict := cfg.SSH.StrictHostKey == nil || *cfg.SSH.StrictHostKey
	ag, err := builtinAgent(cfg)
	if err != nil {
		return ssh.Options{}, err
	}
	opts := ssh.Options{
		StrictHostKey:  strict,
		KnownHostsFile: cfg.SSH.KnownHostsFile,
		AcceptNew:      cfg.SSH.AcceptNew == nil || *cfg.SSH.AcceptNew,
		ForwardAgent:   cfg.SSH.ForwardAgent != nil && *cfg.SSH.ForwardAgent,
		ForwardKey:     cfg.SSH.ForwardKey,
		Agent:          ag,
	}
	if b := cfg.SSH.Bastion; b != nil {
		opts.Bastion = ssh.NewBastion(ssh.Target{
			Host:         b.Address,
			Port:         b.Port,
			User:         b.User,
			IdentityFile: b.IdentityFile,
			Passphrase:   b.IdentityFilePassphrase,
		})
	}
	return opts, nil
}

// builtinAgent assembles the in-memory agent from ssh.identity_file and ssh.identities, or returns nil when neither
// is configured (the transport then falls back to the system ssh-agent).
func builtinAgent(cfg *ast.DeployFile) (ssh.Agent, error) {
	if cfg.SSH.IdentityFile == "" && len(cfg.SSH.Identities) == 0 {
		return nil, nil
	}
	ids := make([]ssh.Identity, 0, len(cfg.SSH.Identities)+1)
	if cfg.SSH.IdentityFile != "" {
		ids = append(ids, ssh.Identity{Name: "identity_file", Path: cfg.SSH.IdentityFile, Passphrase: cfg.SSH.IdentityFilePassphrase})
	}
	for _, name := range sortedKeys(cfg.SSH.Identities) {
		id := cfg.SSH.Identities[name]
		ids = append(ids, ssh.Identity{
			Name:       name,
			Path:       id.Path,
			Content:    id.Content,
			Passphrase: id.Passphrase,
			Recursive:  id.Recursive,
		})
	}
	ag, err := ssh.NewAgent(ids)
	if err != nil {
		return nil, errors.Config("%s", err)
	}
	return ag, nil
}
