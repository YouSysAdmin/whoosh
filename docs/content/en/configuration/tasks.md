---
title: "Tasks"
description: "Named units of work - cmds, scripts, deps, roles, working directory, per-task env, and the targeting/behavior flags."
weight: 50
---

A task is a named unit of work. `cmds` run first, then `scripts`, both in listed order.

```yaml
tasks:
  migrate:
    desc: Run database migrations
    roles: [ db ]
    once: true # run on a single db host, not all of them
    cmds:
      - bundle exec rails db:migrate # runs in the release dir by default
```

| Field                      | Description                                                                                                                                                                                  |
|----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `desc`                     | One-line description (shown in `--help`).                                                                                                                                                    |
| `cmds`                     | Shell command lines (Go-templated). Run first, in order.                                                                                                                                     |
| `scripts`                  | Shell scripts - see [Scripts](/configuration/scripts/). Run after `cmds`.                                                                                                                    |
| `deps`                     | Other task names to run **first**, in order (cycle-detected).                                                                                                                                |
| `dir`                      | Working directory override (default: the release dir). Go-templated, so `dir: "{{.deploy_to}}"` / `"{{.shared_path}}"` work - useful for early hooks that run before the release dir exists. |
| `envs`                     | Per-task environment, overriding the global `envs` per key.                                                                                                                                  |
| `roles`                    | Restrict to hosts with these roles.                                                                                                                                                          |
| `local`                    | Run on the operator's machine instead of the targets.                                                                                                                                        |
| `once`                     | Run on a single host per role, not all of them.                                                                                                                                              |
| `silent`                   | Suppress the task's start announcement (the `task name=...` log line). Output is still shown.                                                                                                |
| `silent_output`            | Hide the task's command output (and echoed commands): buffered, discarded on success, and printed (redacted) only if the task fails. The task is still announced. See below.                 |
| `continue_on_error`        | Don't fail the run if this task's command exits non-zero.                                                                                                                                    |
| `non_deploy` / `all_hosts` | Change which hosts the task targets - see [Hosts -> Inventory vs deploy](/configuration/hosts/#inventory-vs-deploy-targets).                                                                 |
| `strict_host_key`          | Override `ssh.strict_host_key` for this task's connections.                                                                                                                                  |
| `hidden`                   | Omit from the CLI listing (still runnable directly + as dep/hook).                                                                                                                           |
| `only` / `except`          | Limit the task to (or exclude it from) specific stages - see [Per-stage activation](#per-stage-activation-only--except).                                                                     |
| `replace`                  | Run this task in place of a phase's built-in command. Only `deploy:rollback` is replaceable - see [Hooks & phases](/configuration/hooks/).                                                   |
| `output`                   | Capture stdout as run state - see [Task state](/configuration/task-state/).                                                                                                                  |
| `action` / `with`          | Invoke a plugin action - see [Plugins](/configuration/plugins/). Mutually exclusive with `cmds`/`scripts`. `with:` string values are Go-templated (`name: "{{ .asg_name }}"`).               |

## Working directory

Remote task/hook commands - and ad-hoc `run` - execute **inside the release directory** by default (the in-progress
release during a deploy, the live `current` otherwise), so `bundle`/`rails` find the app without a `cd`.
Override with `dir:`. Local (`local: true`) tasks run in your machine's cwd.
A hook that runs *before the release exists* (before `deploy:updating`) must set its own `dir:`.

## Dependencies

List other task names. They run fully, in order, before this task, with cycle detection.
This is the task->task dependency mechanism - independent of hooks (which bind tasks to lifecycle *phases*).
Note: deps run **every time** the task is invoked - there is no per-run de-duplication.

## Hidden tasks

Keep helper tasks (a `setup` dep, a hook-only `restore-manifest`) out of `whoosh
<stage> --help` while leaving them runnable by name and usable as a dep or hook
target with `hidden: true`.

## Quiet tasks

A noisy task (asset precompile, env rendering, a chatty healthcheck) can hide its
command output without hiding *whether it ran or failed*.
With `silent_output:true` the task is still announced, but its output (and the echoed commands) is **buffered** instead
of streamed: discarded on success, and printed - still redacted - only if the task fails, so failure details are never
lost.

```yaml
tasks:
  warm-cache:
    silent_output: true
    cmds: [ ./bin/warm-cache ]   # quiet when it works, full output on failure
```

This is distinct from `silent`, which suppresses the start announcement but keeps the output.
The two are independent and can be combined.

{{< callout type="note" >}}
Notes: ignored under `--dry-run` (plans always print). With `continue_on_error` the run "succeeds",
so the buffer is discarded - per-host failures still surface as warnings, but the command output is not shown.
Don't combine the two when you need the failure detail.
{{< /callout >}}

## Per-stage activation

A task can be limited to (or excluded from) specific stages - the same
filter [plugins](/configuration/plugins/#per-stage-activation-only--except) use:

```yaml
tasks:
  bake-ami:
    only: [ production, uat ]     # runs only in these stages
    action: aws:ec2:ami:create
    with: { name_prefix: myapp, asg: my-asg }
  warm-cache:
    except: [ staging ]           # runs everywhere except staging
    roles: [ app ]
    cmds: [ "bin/rails cache:warm" ]
```

- **`only`** lists the stages the task runs in (empty = all).
- **`except`** lists stages to skip. If both are set, **`except` wins**.
- When a task is **inactive** for the current stage it is **skipped** (logged), not run - whether you invoke it
  directly (`whoosh staging warm-cache`), it's pulled in as a `dep`, or a hook names it.
  So a shared hook like `deploy:published: [restart, warm-cache]` runs `restart` everywhere and skips `warm-cache` on
  `staging`, with no per-stage hook lists.
- An inactive task is also omitted from that stage's `whoosh <stage> --help` listing (like `hidden`), but stays a
  known command so hooks/deps resolve.

