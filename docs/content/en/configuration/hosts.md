---
title: "Hosts"
description: "Hosts, roles, SSH connection settings, local execution mode, inventory-vs-deploy targets, and the unreachable-host policy."
weight: 20
---

A host is one deploy target, tagged with the roles it fills.
Hosts usually live in the per-stage file, with connection defaults in the shared `ssh:` block.

```yaml
hosts:
  - address: web1.example.com
    roles: [ app, web ]
  - address: db1.example.com
    roles: [ db ]
    user: dbadmin          # override ssh.user for this host
    port: 2222             # override ssh.port
    identity_file: ~/.ssh/db_key
```

| Field                             | Description                                                                                          |
|-----------------------------------|------------------------------------------------------------------------------------------------------|
| `address`                         | Hostname or IP (required unless `local: true`).                                                      |
| `roles`                           | Roles this host fills, tasks/hooks target by role.                                                   |
| `user` / `port` / `identity_file` | Per-host SSH overrides.                                                                              |
| `identity_file_passphrase`        | Decrypts this host's encrypted `identity_file` (templatable, redacted everywhere).                   |
| `local`                           | Run on the operator's machine via the local shell - see [local mode](#local-execution-mode).         |
| `deploy`                          | `false` keeps the host in inventory without deploying - see [below](#inventory-vs-deploy-targets).   |
| `required`                        | `true` makes its unreachability always fatal - see [unreachable](#unreachable-hosts-on_unreachable). |

Roles let one task hit a subset of the fleet: a task with `roles: [db]` runs only on db hosts.
The `--roles` / `--host` flags narrow any action further (see [Usage -> Targeting](/usage/#targeting-roles-and-host)).

## SSH connection settings

Defaults applied to every host. A host may override `user`/`port`/`identity_file`.

```yaml
ssh:
  user: deploy
  port: 22
  identity_file: ~/.ssh/deploy_key     # optional, joins the builtin agent (see below)
  identity_file_passphrase: '{{ envSecret "DEPLOY_KEY_PASS" }}'  # decrypts an encrypted identity_file
  known_hosts_file: ~/.ssh/known_hosts # optional custom path
  strict_host_key: true                # verify host keys (default true)
  accept_new: true                     # trust first-seen hosts, record their key (default true)
  forward_agent: false                 # forward an ssh-agent to the host (for remote git auth)
  forward_key: ~/.ssh/deploy           # OR forward just this one key, in-memory
  identities:                          # optional, feeds the builtin in-memory agent
    worker_hosts:
      path: ~/.ssh/id_worker           # a key file
    app_hosts:
      content: '{{ envSecret "APP_DEPLOY_KEY" }}'    # or the key PEM inline, e.g. from the env
      passphrase: '{{ envSecret "APP_KEY_PASS" }}'   # decrypts an encrypted key
    all_keys:
      path: ~/.ssh                     # or a directory: every key file in it is loaded
      recursive: true                  # include subdirectories
```

- **Auth**: with no `identity_file` and no `identities`, whoosh uses your `ssh-agent` (`SSH_AUTH_SOCK`).
  When either is set, whoosh builds its own in-memory agent from those keys and the system agent is **not**
  consulted - so CI and multi-key setups need no `ssh-agent` on the operator machine.
- **Builtin agent (`identities`)**: each entry is a key source, the name is just a label for logs and errors.
  Set exactly one of `path` (a key file, or a directory whose key files are all loaded - `recursive` descends into
  subdirectories) or `content` (the key PEM inline).
  All keys are offered to every host, like a real agent, and per-host `identity_file` overrides keep working.
  An encrypted key needs `passphrase` - the field is a Go template rendered at load time, so it can come from the
  environment or an [env file](/configuration/overview/#env_files) via `envSecret`.
  A directory scan skips non-key files and encrypted keys it cannot open (with a warning), while an explicit file or
  inline key that fails to load is a hard error.
  `content` and `passphrase` are always redacted in the `config` dump, `{{.config}}`, and logs.
- **Encrypted identity files**: `identity_file_passphrase` decrypts an encrypted `identity_file`, at the `ssh:` level
  or per host. Like the identities' `passphrase`, it is a Go template rendered at load time (so it can come from
  `envSecret`) and always redacted.
  A host inherits the global passphrase only together with the global `identity_file` - a host that sets its own
  `identity_file` (even repeating the global path) sets its own passphrase, so a wrong global passphrase is never
  tried against a different key.
  Hosts discovered by an inventory plugin arrive after load-time rendering: a passphrase the plugin sets is used
  verbatim, only the inherited global one is templated.
- **Host keys** are verified against `~/.ssh/known_hosts` by default, OpenSSH `accept-new` style: a host seen for
  the first time is trusted and its key appended to the known_hosts file (created, along with its directory, when
  missing), while a **changed** key fails - so fresh environments (containers, CI) work out of the box without losing
  protection against key swaps.
  Set `accept_new: false` to require every host key to already be present (strictest; pre-populate with
  `ssh-keyscan`), `strict_host_key: false` to skip verification entirely, or `known_hosts_file` for a custom path.
  A single task can override this with its own `strict_host_key: false` (see [Tasks](/configuration/tasks/)) - for
  ephemeral hosts whose key is legitimately unknown (e.g.
  ASG instances from one AMI), without loosening the rest of the deploy.
- **Agent forwarding** lets the remote `git` clone/fetch authenticate with *your* credentials (e.g. for a private
  repo).
  With `forward_agent: true`, the builtin agent is forwarded when it is active, otherwise your local ssh-agent
  (`SSH_AUTH_SOCK`) - so forwarding with `identities` needs no system agent either.
  `forward_key` forwards a single unencrypted key in memory (never written to the host) and takes precedence over
  both.
  Forwarding is best-effort: a host with `AllowAgentForwarding no` will still run, but its git won't see your keys.
- **Liveness**: a new connection times out after ~15s.
  On an established connection whoosh sends a keepalive every 10s and drops a silent host after 3 misses (~30s) so a
  dead host fails fast instead of hanging.

## Local execution mode

Mark a host `local: true` to run the entire lifecycle on the current machine through the local shell - no SSH, keys,
or `known_hosts` needed. Useful for deploying on the box itself, or for development.
Everything else (releases, symlinks, `current`, rollback, tasks, hooks) is identical.

```yaml
# deploy/local.yml
hosts:
  - address: localhost
    local: true
    roles: [ app, web, db ]
```

Local and remote hosts can coexist in one stage, each running over its own transport.

## Inventory vs deploy targets

By default, every host is a deployment target.
Set `deploy: false` to keep a host in the **inventory** - visible in `config` and the `deploy:hosts` table - without
deploying to it. Such hosts are excluded from the lifecycle, tasks, hooks, and ad-hoc `run`.

```yaml
hosts:
  - address: web1.example.com
    roles: [ app, web ]
  - address: bastion.example.com
    roles: [ ops ]
    deploy: false        # listed, but never deployed to
```

This pairs with dynamic inventory: a plugin can discover a fleet and flag which hosts
to deploy to (see [`aws:ec2:inventory`](/plugins/aws/#awsec2inventory) `deploy_tag`).

Two task flags change which hosts a task targets:

- **`non_deploy: true`** - only the `deploy:false` hosts (inverts the default).
  For acting on hosts you don't deploy to, e.g. healthchecking ASG instances booted from a baked AMI.
- **`all_hosts: true`** - every host, ignoring the `deploy` flag.
  For a task that should hit the whole fleet (e.g. collecting disk usage). Wins over `non_deploy`.

Roles and `--roles`/`--host` still narrow within the chosen set.

```yaml
tasks:
  asg-healthcheck:
    non_deploy: true            # only deploy:false hosts
    strict_host_key: false      # ASG hosts share a key / rotate IPs -> skip known_hosts
    scripts:
      - path: healthcheck.sh
  disk-usage:
    all_hosts: true             # every host in the stage
    cmds: [ "df -h /" ]
```

A task's **`strict_host_key`** (`true`/`false`) overrides the stage's `ssh.strict_host_key` for just that task's
connections - set `false` for ephemeral hosts whose key isn't in `known_hosts` (the override applies when the
connection is first opened in a run).

{{< callout type="warning" title="Inventory is captured at process start" >}}
Both flags operate on the inventory captured **at process start**.
A fresh run re-fetches dynamic inventory, so instances created during a deployment (e.g. by an ASG refresh) only appear
on
the next invocation, not to a hook within the same deploy.
Run these as their own post-deploy step (`whoosh prod asg-healthcheck`), not as a deploy hook.
{{< /callout >}}

## Unreachable hosts

By default, a host that becomes unreachable mid-deploy aborts the whole run.
Set `on_unreachable: skip` to drop the unreachable host and finish on the survivors.

```yaml
on_unreachable: skip        # abort (default) | skip
hosts:
  - address: db1.example.com
    roles: [ db ]
    required: true          # never skip this one - its loss always aborts
  - address: web1.example.com
    roles: [ app, web ]       # skippable under `skip`
```

- **`abort`** (default) - any unreachable host fails the deploy.
- **`skip`** - an unreachable host is dropped (from remaining phases *and* hook tasks), the deploy completes on the
  rest, skipped hosts are logged, and the command exits **non-zero** so CI notices.
- `required: true` keeps a critical host from being skipped.
  The lock-holding primary host is always implicitly required.
- The policy applies to **unreachable** hosts only (a failed dial or a connection lost mid-command).
  A command that *runs and exits non-zero* (e.g. a failed migration) always aborts - use a task's `continue_on_error`
  to soften those.
