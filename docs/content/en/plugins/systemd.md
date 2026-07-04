---
title: "systemd"
description: "The bundled systemd plugin - start/stop/restart/enable/disable units and daemon-reload on the deploy hosts, ad-hoc or auto-wired to a deploy phase."
weight: 7
---

Manages systemd units on the deploy hosts through six actions:

| Action                  | Runs                              |
|-------------------------|-----------------------------------|
| `systemd:start`         | `systemctl start <units>`         |
| `systemd:stop`          | `systemctl stop <units>`          |
| `systemd:restart`       | `systemctl restart <units>`       |
| `systemd:enable`        | `systemctl enable <units>`        |
| `systemd:disable`       | `systemctl disable <units>`       |
| `systemd:daemon-reload` | `systemctl daemon-reload`         |

The plugin is **bundled and default-on**: the actions are available in every binary without a `plugins:` entry.
Disable it per stage like any default-on plugin:

```yaml
plugins:
  - name: systemd
    enabled: false
```

## Ad-hoc: from any task

Invoke an action with `action:`/`with:` - the commands run on the hosts the task targets (`roles:`, `--host`, etc.),
in parallel, echoed per host like any task command:

```yaml
tasks:
  restart-services:
    desc: Restart the app services
    roles: [app]
    action: systemd:restart
    with:
      system_unit_files: [app, sidekiq.target]
      sudo: true
      daemon_reload: true    # `systemctl daemon-reload` first (fresh unit files)
```

## Auto-wired: as a deploy hook

A `plugins:` entry whose `actions:` params set `phase:` contributes a hidden task invoking that action and anchors it
**before** (default) or **after** the phase - no `hooks:` wiring needed. The same action may appear in several
entries (e.g. stop early, start late):

```yaml
plugins:
  - name: systemd
    params:                        # global defaults for every systemd action
      sudo: true
    actions:
      - name: systemd:stop
        params:
          system_unit_files: [app]
          phase: "deploy:publishing"   # stop just before the symlink swap
      - name: systemd:start
        params:
          system_unit_files: [app]
          daemon_reload: true
          phase: "deploy:published"    # start once the new release is live
          when: "after"
          roles: [web]                 # restrict the hook task to these hosts
```

## Params

Layering: a task's `with:` wins over the matching `actions:` entry's params, which win over the plugin's global
`params:`.

| Param               | Default    | Meaning                                                                                                    |
|---------------------|------------|------------------------------------------------------------------------------------------------------------|
| `system_unit_files` | `[]`       | Units managed via the system manager.                                                                       |
| `user_unit_files`   | `[]`       | Units managed via `systemctl --user` (never sudo'd - the user manager belongs to the SSH user).             |
| `sudo`              | `false`    | Prefix system-manager commands with `sudo -n`.                                                              |
| `daemon_reload`     | `false`    | Run `systemctl daemon-reload` (per scope in use) before the verb.                                           |
| `now`               | `false`    | `enable`/`disable` only: add `--now` (also start/stop the units).                                           |
| `no_block`          | `false`    | Add `--no-block`: enqueue the job without waiting for it to finish.                                         |
| `roles`             | `[]`       | Plugin `actions:` only - restrict the contributed hook task to hosts with these roles.                      |
| `phase`             | `""`       | Plugin `actions:` only - phase to anchor the hook task to; empty contributes no hook (defaults only).       |
| `when`              | `"before"` | Plugin `actions:` only - `"before"` or `"after"` the phase.                                                  |

The standalone `systemd:daemon-reload` action ignores the unit lists and takes its own scope params:
`system: true` (default) reloads the system manager, `user: true` reloads the user manager.

Unit names are validated (`[A-Za-z0-9@:._-]` only - `app`, `sidekiq.target`, `worker@1.service` are all fine), so a
typo'd or malicious name fails before anything reaches a shell.

## Requirements on the hosts

- **system units** need a privilege grant for the deploy user - via **polkit** (recommended, keep `sudo: false`) or a
  **sudoers** rule (`sudo: true`); see below.
- **user units**: `systemctl --user` over SSH needs a running user manager - enable lingering
  (`loginctl enable-linger <user>`) and make sure `XDG_RUNTIME_DIR` is set for non-interactive sessions.

### Granting access with polkit (recommended)

Let the deploy user manage *specific units* directly and keep `sudo: false` - root is never involved, and the grant
is scoped per unit **and** per verb:

```js
// /etc/polkit-1/rules.d/49-whoosh-deploy.rules
polkit.addRule(function (action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        subject.user == "deploy") {
        var unit = action.lookup("unit");
        var verb = action.lookup("verb"); // start / stop / restart / ...
        if (["app.service", "sidekiq.target"].indexOf(unit) >= 0 &&
            ["start", "stop", "restart"].indexOf(verb) >= 0) {
            return polkit.Result.YES;
        }
    }
});
```

```yaml
tasks:
  restart-services:
    roles: [app]
    action: systemd:restart
    with:
      system_unit_files: [app, sidekiq.target]   # no sudo needed
```

Notes:

- polkit sees the **normalized** unit name - `system_unit_files: [app]` reaches the rule as `app.service`.
- `enable`/`disable` are unit-*file* operations - additionally allow `org.freedesktop.systemd1.manage-unit-files`;
  `daemon-reload` is `org.freedesktop.systemd1.reload-daemon`.
- JavaScript `rules.d` needs polkit >= 106 (current RHEL/Fedora/Arch/Debian 12+/Ubuntu 22.04+); the old polkit 105
  `.pkla` format can't express per-unit rules - use the sudoers option there.

### Granting access with sudoers

With `sudo: true` the plugin prefixes system-manager commands with `sudo -n` (non-interactive - a missing rule fails
fast instead of hanging on a password prompt), so the deploy user needs a `NOPASSWD` rule. Grant the exact commands
in a drop-in:

```
# /etc/sudoers.d/whoosh-deploy   (validate with: visudo -cf /etc/sudoers.d/whoosh-deploy)
deploy ALL=(root) NOPASSWD: \
    /usr/bin/systemctl daemon-reload, \
    /usr/bin/systemctl start app sidekiq.target, \
    /usr/bin/systemctl stop app sidekiq.target, \
    /usr/bin/systemctl restart app sidekiq.target
```

Notes:

- sudoers matches the **whole argv**: the plugin runs `systemctl <verb> [--now] [--no-block] <units...>` with the
  units in `system_unit_files` order, so grant that exact line - flags included, if you use `now`/`no_block` - or
  keep one unit per action entry. The unit names appear verbatim - `app`, not `app.service`, if that is what the
  Deployfile says.
- Avoid wildcards (`/usr/bin/systemctl restart *`): in sudoers `*` also matches spaces, i.e. arbitrary extra
  arguments and units.
- Check the `systemctl` path with `command -v systemctl` - some distros install it as `/bin/systemctl`.
