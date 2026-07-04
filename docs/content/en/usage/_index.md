---
title: "Usage"
description: "The CLI: commands, global flags, targeting, dry-run, the deploy lifecycle, rollback, concurrency, locking, logging and secret masking."
weight: 40
icon: terminal
---

Every invocation has the shape:

```
whoosh <stage> <action> [flags]
```

The **stage** is the name of a `deploy/<stage>.yml` file (it's just data, so any file you add becomes a usable stage).
The **action** is a built-in command, or the name of a task from your Deployfile.
The stage-less commands are `whoosh init`, `whoosh version`, `whoosh plugins`, and `whoosh build`.

## Commands

| Command                                      | Description                                                                                                                                |
|----------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `whoosh init`                                | Scaffold `Whooshfile.yml`, `whoosh/<stage>.yml` files, and `whoosh/scripts/` (the `Deployfile.yml` + `deploy/` spelling is also accepted). |
| `whoosh version`                             | Print the version (and the compiled-in plugin versions).                                                                                   |
| `whoosh plugins`                             | List the plugins compiled into this binary.                                                                                                |
| `whoosh build`                               | Compose a custom binary with extra plugin modules - see [Installation -> With custom plugins](/installation/custom-plugins/).              |
| `whoosh <stage> deploy`                      | Build and publish a new release.                                                                                                           |
| `whoosh <stage> deploy:rollback [--cleanup]` | Repoint `current` at the previous release (`--cleanup` removes the rolled-back release).                                                   |
| `whoosh <stage> deploy:check`                | Validate connectivity, ensure the directory tree exists, and verify every `linked_files` entry is present in `shared/`.                    |
| `whoosh <stage> deploy:unlock`               | Clear a stale deploy lock on the primary host.                                                                                             |
| `whoosh <stage> releases`                    | List the releases present on each host.                                                                                                    |
| `whoosh <stage> deploy:hosts`                | Print the stage's resolved hosts (including `deploy: false`) as a table. Provided by the default-on `print-hosts-table` plugin.            |
| `whoosh <stage> config`                      | Print the fully resolved, merged configuration.                                                                                            |
| `whoosh <stage> validate`                    | Validate the configuration.                                                                                                                |
| `whoosh <stage> run "<cmd>"`                 | Run an ad-hoc command on the stage's hosts.                                                                                                |
| `whoosh <stage> <task>`                      | Run a named task from the Deployfile.                                                                                                      |

Task commands are discovered from your Deployfile per stage, so `whoosh production --help` lists the tasks available
for that stage (tasks marked `hidden: true` are omitted but still runnable).

## Global flags

Available on any `<stage> <action>`:

| Flag                  | Meaning                                                                 |
|-----------------------|-------------------------------------------------------------------------|
| `--dry-run`           | Print the exact plan without contacting a host (see [below](#dry-run)). |
| `-v`, `--verbose`     | Verbose output (logs each command before running it).                   |
| `--roles <r1,r2>`     | Restrict to hosts filling these roles.                                  |
| `-H`, `--host <host>` | Restrict to specific hosts (repeatable / comma-separated).              |
| `--concurrency <n>`   | Max hosts to run a command on at once (`0` = all, the default).         |
| `--deployfile <path>` | Use a specific Deployfile instead of auto-discovery.                    |

Logging flags:

| Flag                | Meaning                                                                                                  |
|---------------------|----------------------------------------------------------------------------------------------------------|
| `--log-level`       | `debug` / `info` (default) / `warn` / `error`.                                                           |
| `--log-format`      | `text` (default) or `json`.                                                                              |
| `--log-output`      | `stdout`, `stderr`, or a file path.                                                                      |
| `--log-color`       | On by default, auto-suppressed when output is a file or a pipe.                                          |
| `--log-file`        | **Also** write a deploy log to this file, in addition to `--log-output`. Empty (default) = console only. |
| `--log-file-format` | Format for `--log-file`: `text` (default) or `json`. The file is never colorized.                        |

`--log-file` adds a *second* destination rather than replacing the console one, so a single run keeps colored output
on the terminal **and** a log on disk. What the file contains depends on `--log-file-format`:

- **`text` (default) - full transcript.**
  Whoosh's narrative (phases, tasks, results) **and** all host command output - the raw remote/local output that
  normally streams only to stdout - exactly as the console shows it, minus color.
  This is what you want to capture a complete record of a deploy (e.g. why an asset compile failed on one host).
- **`json` - narrative only.** Whoosh's events as one JSON object per line, for machine parsing / log shipping.
  Host **command output is deliberately excluded**: interleaving raw bytes would break the JSON lines.
  Use `text` if you need the command output captured.

```sh
whoosh production deploy --log-file deploy.log
# console: colored text   -   deploy.log: full transcript (narrative + command output)

whoosh production deploy --log-file events.json --log-file-format json
# console: colored text   -   events.json: structured narrative, one JSON object per line
```

(Secret masking applies to the file too - command output written to the transcript goes through the same redactor as
the console.)

### Log settings in the Deployfile

The `--log-*` flags can also be set in the Deployfile under `log:`, so a project logs consistently without repeating
flags. A command-line `--log-*` flag you set explicitly always **overrides** the Deployfile value:

```yaml
log:
  level: info             # debug, info, warn, error
  format: json            # text or json
  output: stdout          # stdout, stderr, or a file path
  color: true             # colorize text logs (terminal only)
  file: deploy.log        # also write a deploy log here (like --log-file)
  file_format: text       # text or json for `file`
  raw_remote_log: true    # true (default): stream command output raw, false: emit it through the logger
```

Put `log:` in `deploy/<stage>.yml` to vary logging per stage (e.g. JSON in CI).

### Command output as structured logs (`raw_remote_log`)

By default whoosh streams host command output **raw**, prefixed by host, as it arrives - and the `json` log channel
deliberately excludes it (raw bytes would break the JSON lines). That is great for a human watching a terminal, but
means a `json` log stream you ship to a collector doesn't contain what the commands actually printed.

Set `raw_remote_log: false` to flip this: each line of command output is emitted **through the logger** as a structured
record instead of streamed raw, so it joins the JSON stream and can be shipped and parsed. The echoed command becomes an
`exec` record too. For example, with `log.format: json` and `raw_remote_log: false`:

```json
{
  "time": "...",
  "level": "INFO",
  "msg": "exec",
  "task": "hello",
  "host": "local",
  "command": "echo hi"
}
{
  "time": "...",
  "level": "INFO",
  "msg": "output",
  "task": "hello",
  "host": "10.4.20.204",
  "output": "hi"
}
```

Each record carries the `host` it came from and the `task` that produced it. masking still applies, so secrets stay
masked in the shipped logs. (Dry-run plans are unaffected - they remain raw, since they are for interactive inspection.)

## Targeting: roles and host

A task with `roles: [db]` already runs only on db hosts. The flags narrow any action further, on top of that:

```sh
whoosh production deploy --roles web        # only web hosts
whoosh production deploy --host web1.example.com
whoosh production migrate                   # the task's own roles: [db] applies
whoosh production run "uptime" --roles app
```

## Dry run

`--dry-run` prints the complete plan - every command, on every host, with the rendered environment - and contacts
**no** host:

```sh
whoosh production deploy --dry-run
```

Use it to review a change before applying it, or as a CI pre-step.
In dry-run, run-time-only values (like captured [task state](/configuration/task-state/)) render as `<no value>`
instead of erroring, and action tasks print their call without reaching AWS.
(Note: startup plugins like `aws:ec2:inventory` *do* run on every command, including dry-run, since they populate the
host list.)

## Deploy lifecycle

`whoosh <stage> deploy` runs these phases in order, each as a barrier across all target hosts (with your
`hooks.before`/`hooks.after` wrapped around it):

| Phase               | What it does                                                                                                                                                       |
|---------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `deploy:starting`   | Acquire the deploy lock on the primary host.                                                                                                                       |
| `deploy:check`      | Ensure the `<deploy_to>` directory tree exists (creating `linked_dirs`), and verify every `linked_files` entry exists in `shared/` - fail early if one is missing. |
| `deploy:init`       | *(marker - provision the host: install software / deps. Hook tasks need an explicit `dir:`, e.g. `"{{.deploy_to}}"`.)*                                             |
| `deploy:started`    | *(marker - hook anchor, no built-in step)*                                                                                                                         |
| `deploy:updating`   | Update the git mirror, create the release, record `REVISION`/`REVISION_TIME`, resolve the commit SHA.                                                              |
| `deploy:symlink`    | Link `linked_files`/`linked_dirs` from `shared/` into the new release.                                                                                             |
| `deploy:updated`    | *(marker - "release built & linked, not yet live")*                                                                                                                |
| `deploy:publishing` | Atomically swap the `current` symlink to the new release.                                                                                                          |
| `deploy:published`  | *(marker - "release is live")*                                                                                                                                     |
| `deploy:finishing`  | Append to `revisions.log`, prune old releases beyond `keep_releases`.                                                                                              |
| `deploy:finished`   | *(marker - done)*                                                                                                                                                  |

On any step or hook error, the special `deploy:failed` hook runs (best-effort, for notifications) and the deploy
returns the error. See [Configuration -> Hooks & phases](/configuration/hooks/) for attaching tasks.

### On-target layout

```
<deploy_to>/
  repo/                       # git mirror cache
  releases/<timestamp>/       # one dir per release
    REVISION                  # deployed git SHA
    REVISION_TIME             # deploy time (RFC3339)
  shared/                     # linked_files / linked_dirs persist here
  current -> releases/<timestamp>
  revisions.log               # one line appended per deploy
```

Each deploy appends to `revisions.log`: `Branch main (at <sha>) deployed as release <timestamp> by <user>`.

## Rollback

```sh
whoosh production deploy:rollback            # repoint current at the previous release
whoosh production deploy:rollback --cleanup  # also delete the rolled-back release
whoosh production releases                   # inspect what's available first
```

Rollback fires `before`/`after` `deploy:rollback` hooks around the swap.
The `after` hooks run with `current` already pointing at the restored release (use them to fix up shared state - see
[`examples/07-rails-assets`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/07-rails-assets)).
Rolling back past the oldest release fails cleanly.

## Concurrency

For a given command, all target hosts run in **parallel**, with a barrier between phases (output is streamed, prefixed
by host). Bound the fan-out with `--concurrency`:

```sh
whoosh production deploy --concurrency 5     # at most 5 hosts at a time
```

`0` (default) means unbounded - all hosts at once. Within a single host, steps run sequentially.

## Locking

A deploy takes a lock on the primary (first) host to block concurrent deploys of the same stage.
If a deploy is interrupted and leaves a stale lock:

```sh
whoosh production deploy:unlock
```

The lock-holding primary is implicitly `required`, so it's never dropped by `on_unreachable: skip`.

## Logging & secret masking

Whoosh's own narrative (phases, tasks, results, warnings) goes through `slog`.
Raw remote/local command output and structured dumps (`config`, `releases`, `--dry-run` plans) stream to stdout,
prefixed by host.

**Each task command is echoed before it runs**, host-prefixed, so the console and the `--log-file` transcript show
*what was sent* to the host, not just its output:

```
[10.0.0.5] $ bundle exec rake assets:precompile
[10.0.0.5] ... compiling ...
```

(`cmds` are echoed. Multi-line `scripts` are announced by name and only echoed in full under `--verbose`.
Built-in lifecycle commands - git, symlink swaps - also echo only under `--verbose`.)

When color is enabled (`--log-color`, on by default) **and** the output is a terminal, the `[host]` prefix is shown in
**green** on both the command's stdout and its stderr - the prefix marks *which host* a line came from, not severity.
(It deliberately doesn't turn red on stderr: tools like `git` and `npm` write normal progress to stderr, and each line
streams before the command's exit status is known, so the prefix can't reliably flag failure. A command that actually
fails still surfaces in red through whoosh's own `ERROR`/`WARN` log line.) Color is suppressed when output is
redirected, piped, or tee'd to `--log-file`, so transcripts never get ANSI codes. (With `raw_remote_log: false` there is
no raw prefix to color - output becomes structured log records instead.)

**Secret masking**: command output, the echoed commands, dry-run plans, and verbose command logs are scrubbed before
reaching the console (or the transcript file). Two layers:

- **Built-in patterns** for well-known secret formats - AWS keys, GitHub/Slack/Stripe/SendGrid/Google/npm tokens,
  JWTs, `key=secret` pairs, and credentials embedded in URLs (`https://user:TOKEN@host` ->
  `https://user:[FILTERED]@host`). Pattern-based and best-effort.
- **User-marked secrets** for anything the patterns don't recognize: mark a value sensitive in a template and its
  exact value is masked everywhere whoosh prints it.

  | Helper | Use |
      | --- | --- |
  | `{{ envSecret "NAME" }}` | Like sprig's `env`, but the value is registered as sensitive and always redacted. |
  | `{{ sensitive .db_password }}` | Mark any value (a var, an expression) sensitive. |

  ```yaml
  cmds:
    # the token is used in the command but shows as [FILTERED] in logs/echo:
    - bundle config set --global rubygems.pkg.github.com {{ envSecret "REG_TOKEN" }}
  ```

(Function names can't contain `-`, so it's `envSecret`, not `env-sens`.
Values shorter than 4 characters are ignored so a near-empty var can't blank the logs.)

{{< callout type="warning" title="Debug disables masking" >}}
masking is **turned off at `--log-level debug`** so you can see raw output when debugging - including user-marked
secrets. Don't ship debug logs.
{{< /callout >}}

## Cancellation & liveness

`Ctrl-C` / `SIGTERM` cancels cleanly: in-flight commands are signaled and the deploy lock is released (deferred
cleanup runs), rather than the process being killed outright.

A host that dies mid-deploy (power loss, partition) surfaces as an error and fails the run fast instead of hanging: a
new connection times out after ~15s, and on an established connection whoosh sends a keepalive every 10s, dropping the
host after ~30s of silence.
To finish on the survivors instead of aborting, use [`on_unreachable:
skip`](/configuration/hosts/#unreachable-hosts-on_unreachable).

## Common workflows

**First deploy to a new stage:**

```sh
whoosh production deploy:check       # connectivity + create the tree
whoosh production deploy --dry-run   # review the plan
whoosh production deploy
```

**Routine deploy:**

```sh
whoosh production deploy
```

**Roll back a bad release:**

```sh
whoosh production deploy:rollback
```

**Run a one-off task or command:**

```sh
whoosh production migrate                 # a Deployfile task
whoosh production run "bin/rails runner 'puts User.count'" --roles app
```

**Inspect without changing anything:**

```sh
whoosh production config                  # resolved, merged config
whoosh production deploy:hosts            # the host table
whoosh production releases                # releases per host
```
