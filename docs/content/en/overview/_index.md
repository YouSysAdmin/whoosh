---
title: "Overview"
description: "What Whoosh is, how a deploy works, and what the single binary does when you run it."
weight: 10
icon: book-open
---

**Whoosh** (formerly known as Deployer) is a single-binary deployment tool.
It connects to your hosts over SSH (or locally), builds a timestamped release from git, links shared files and
directories into it, atomically swaps a `current` symlink, and supports rollback - all driven by a `Deployfile.yml`
plus per-stage `deploy/<stage>.yml` files. (Same directory structure as in Capistrano)

{{< callout type="info" title="Capistrano?" >}}
For those who have used **Capistrano**, this utility may seem familiar.

Indeed, this utility was originally written as a replacement for **Capistrano** and therefore inherited familiar
behavior.
{{< /callout >}}

The command is `whoosh`, and every invocation has the same shape:

```sh
whoosh <stage> <action>
```

## Why it exists

- **No agent on the targets.** There is no daemon, agent, or runtime to install.
  A host only needs `git`, `tar`, a POSIX shell (`/bin/sh`) and your scripts dependencies.
  Whoosh talks SSH directly, so the operator machine doesn't even need the OpenSSH client.
- **One binary to ship.** Build it once, drop it on your `PATH` or in a CI image, and run it anywhere.
- **Batteries included.**
  Releases, `shared/` files, the `current` symlink, rollback, roles, and lifecycle hooks all come built in.

## What it does when you run a deploy

`whoosh <stage> deploy` runs a fixed sequence of phases, each as a barrier across all the stage's hosts, with your
`before`/`after` hooks wrapped around it:

```ascii
   deploy:starting ---> deploy:check ---> deploy:init ---> deploy:started
    ----------------------------------+------------------------------------
                                      |
                                      v

deploy:updating ---> deploy:symlink ---> deploy:updated ---> deploy:publishing
--------------------------------------+---------------------------------------
                                      |
                                      v

        deploy:published ---> deploy:finishing ---> deploy:finished
        -----------------------------------------------------------
```

1. **Lock & check.**
   Take a deploy lock on the primary host, ensure the directory tree exists, and verify every `linked_files` entry is
   present in `shared/`.
2. **Build the release.**
   Update the git mirror, create `releases/<timestamp>/`, and record the deployed `REVISION` / `REVISION_TIME`.
3. **Link & publish.** Symlink the shared files/dirs into the new release, then atomically repoint `current` at it.
4. **Finish.** Append to `revisions.log` and prune releases beyond `keep_releases`.

The marker phases (`deploy:init`, `deploy:started`, `deploy:updated`, `deploy:published`, `deploy:finished`) run no
built-in command - they are just hook anchors for assign your tasks if needed.
See [Usage -> Deploy lifecycle](/usage/#deploy-lifecycle) for what each phase does,
and [Configuration -> Hooks & phases](/configuration/hooks/) for attaching
your own tasks.

## On-target layout

A deploy produces this tree under each host's `deploy_to`:

```
<deploy_to>/
  repo/                       # bare git mirror cache
  releases/<timestamp>/       # one dir per release
    REVISION                  # deployed git SHA
    REVISION_TIME             # deploy time (RFC3339)
  shared/                     # linked_files / linked_dirs persist here
  current -> releases/<timestamp>
  revisions.log               # one line appended per deploy
```

## What you configure

Everything lives in two kinds of YAML file:

- **`Deployfile.yml`** - shared config: the app, defaults, tasks, hooks, plugins.
- **`deploy/<stage>.yml`** - one file per stage, holding that stage's hosts and any overrides (the stage wins - see
  [merge rules](/configuration/overview/#merge-rules)).

A first config is two short files:

```yaml
# Deployfile.yml
version: "1"
app:
  name: myapp
  repo: git@github.com:org/myapp.git
  branch: main
  deploy_to: /var/www/myapp
  keep_releases: 5
```

```yaml
# deploy/production.yml
hosts:
  - address: web1.example.com
    roles: [ app, web ]
  - address: db1.example.com
    roles: [ db ]
```

## Who it's for

Teams who want multi-host, role-aware deploys.

## Next steps

- **[Installation](/installation/)** - build the binary and run it from CI.
- **[Configuration](/configuration/)** - write your `Deployfile.yml`.
- **[Usage](/usage/)** - the commands, flags, lifecycle, and rollback.
- **[Plugins](/plugins/)** - dynamic inventory, AWS, Slack, rbenv, and writing your own.
- **[Examples](/examples/)** - runnable, self-contained configs.
