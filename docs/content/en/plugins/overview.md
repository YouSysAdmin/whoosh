---
title: "Overview"
description: "Using plugins: declaring them, action tasks, enabling/disabling, per-stage activation, templated params, and custom phases."
weight: 5
---

Plugins are compiled into the `whoosh` binary and are self-contained - the core never reaches into a plugin.
You list the ones you want under `plugins:`. Each validates its config on load and registers what it contributes:

- a **startup hook** - runs at load and can append to `hosts:` (dynamic inventory), and/or
- one or more **actions** - invoked by name from a task or hook (`action: <name>`).

```yaml
plugins:
  - name: aws
    params: { region: eu-west-1 }     # the plugin's global config
    actions:                          # per-feature config (plugin-specific)
      - name: aws:ec2:inventory
        params: { tags: { App: [ myapp ] } }
```

Bundled plugins (like the default-on `print-hosts-table`) are in every binary, the separate plugin modules
([`aws`](/plugins/aws/), [`slack`](/plugins/slack/), [`rbenv`](/plugins/rbenv-p/)) are compiled in with
`whoosh build --with ...` - see [Installation -> With custom plugins](/installation/custom-plugins/).
To write your own, see [Developing](/plugins/developing/).

This page covers the config every plugin shares, each plugin's own params and actions are on its page.

## Action tasks

An **action task** invokes a registered action operator-side (on your machine, not over SSH).
It uses `action:` and `with:` instead of `cmds`/`scripts` (the two are mutually exclusive):

```yaml
tasks:
  refresh_asg:
    desc: Roll the ASG and wait for the refresh to finish
    action: aws:ec2:asg:refresh
    with:
      name: my-asg
```

An action task runs once. `--dry-run` prints the call without contacting the outside service.
(Startup plugins like `aws:ec2:inventory` run on *every* command, including dry-run, because they populate the host
list.)

**`with:` values are Go-templated.**
String values (at any depth - nested maps and lists too) are rendered against `vars` and the deploy context before
reaching the plugin, while numbers and booleans pass through untouched.
So you can drive params from `vars` or the stage:

```yaml
vars:
  asg_name: my-asg
tasks:
  refresh_asg:
    action: aws:ec2:asg:refresh
    with:
      name: "{{ .asg_name }}"          # from vars
      # name: "{{ .stage }}-asg"       # or build it from the deploy context
```

Action tasks run operator-side, so there is no host - `{{.host}}` renders empty.

{{< callout type="warning" title="Quote the template" >}}
YAML reads a value starting with `{` as a flow mapping, so `name: {{ .asg_name }}` is a *parse error*.
Always quote it: `name: "{{ .asg_name }}"`.
Likewise quote tag-ish values that look like bools/numbers (`Deploy: "true"`), since the plugin wants strings.
{{< /callout >}}

## Enabling / disabling a plugin

`enabled: false` turns a plugin off entirely - a coarse switch, independent of stage:

```yaml
plugins:
  - name: aws
    enabled: false      # off everywhere, omit (or true) to load it
    params: { ... }
```

A disabled plugin is **not loaded** (its startup hooks and actions never register), and any **action task bound to it
is skipped** (logged), not failed - the same graceful behavior as an `only`/`except`-inactive plugin (below).
Because it is never loaded, a disabled plugin need not even be compiled into the binary.

Some bundled plugins are **on by default** - they load without a `plugins:` entry.
The `print-hosts-table` plugin is one: it prints the resolved hosts table at the start of every deploy (before
`deploy:starting`). Turn a default-on plugin off the same way, by listing it disabled:

```yaml
plugins:
  - name: print-hosts-table
    enabled: false
```

## Per-stage activation

A plugin can be limited to (or excluded from) specific stages:

```yaml
plugins:
  - name: aws
    except: [ staging ]        # active everywhere EXCEPT staging
    # only: [production, uat]  # ...or active ONLY in these (mutually-exclusive style)
    params: { ... }
```

- **`except`** lists stages where the plugin is **off**.
- **`only`** lists the stages where it is **on** (empty = all). If both are set, `except` wins.
- When a plugin is **off** for a stage:
    - it is **not loaded** - its startup hook never runs (e.g. no inventory, and no bastion/credentials contact at all),
      and
    - any **action task bound to it is skipped** (logged), not failed.
      The binding is by namespace: with `aws` off, every `aws:*` action task (`aws:ec2:ami:create`,
      `aws:ec2:asg:refresh`, ...) is skipped.
      Non-action tasks (a `restart`) still run, and a genuinely unknown action still errors (typo safety).

This is how you say *"deploy to staging, but it has no AWS"*: the `aws` plugin is inactive there, so a hook like
`deploy:published: [restart, bake-ami, asg-refresh]` runs `restart` and skips the two AWS tasks.

{{< callout type="note" >}}
Individual **tasks** support the same `only`/`except` filter - see [Tasks -> Per-stage
activation](/configuration/tasks/#per-stage-activation).
Use it to scope a plain `cmds`/`scripts` task (one that doesn't depend on a plugin) to specific stages.
{{< /callout >}}

## Parameterizing plugins with vars

Plugin **`params:` (and per-action `params:`) are Go-templated** - rendered against the stage's `vars` plus the static
config (`{{.stage}}`, `{{.app_name}}`, ...) and sprig helpers (`{{ env "X" }}`).
This lets you keep the **logic in the shared `Deployfile.yml`** and change only **vars per stage** - no duplicated
plugin blocks:

```yaml
# Deployfile.yml (shared) - declared once
plugins:
  - name: aws
    except: [ staging ] # staging has no AWS (see above)
    params:
      region: "{{ .aws_region }}"
      credentials_from_host: { host: "{{ .bastion }}", user: "{{ .deploy_user }}" }
    actions:
      - name: aws:ec2:inventory
        params:
          tags: { Application: [ "{{ .app_name }}" ] }
      - name: aws:ec2:asg
      - name: aws:ec2:ami
```

```yaml
# deploy/uat.yml - only the values differ per stage
vars: { aws_region: ca-central-1, bastion: 10.4.20.204, deploy_user: deployer }
```

```yaml
# deploy/production.yml
vars: { aws_region: us-east-1, bastion: 10.0.1.10, deploy_user: deploy }
```

Notes and limits:

- **Quote the template** for the same YAML reason as above: `host: "{{ .bastion }}"`, not `host: {{ .bastion }}`.
- Plugins load at **startup, before any release exists**, so the context is `vars` + static config + sprig - **not**
  run-time values like `{{.release_path}}` or `{{.commit_hash}}`.
- Rendering is **strict**: an undefined var (`{{ .typo }}`) fails the command with a clear error, rather than silently
  becoming empty.
- A skipped plugin's params are **not** rendered, so a stage where `aws` is off needn't define
  `bastion`/`aws_region`/etc.

[`examples/04-aws-inventory`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/04-aws-inventory) is built
entirely around this pattern - one `aws` plugin declared once, stages that differ only in their `vars`, and a
`staging` stage where `aws` is switched off.

## Custom phases

A **custom phase** is a named phase inserted into the deploy lifecycle before or after a built-in phase.
It runs an optional task and is itself a `before`/`after` hook anchor. Declare it in the Deployfile:

```yaml
custom_phases:
  - name: deploy:migrate
    after: deploy:published    # anchor on a built-in phase (set exactly one of before/after)
    task: run-migrations       # optional, omit for a pure hook anchor

hooks:
  before:
    deploy:migrate: [ notify-db-team ]   # a custom phase is a hook anchor too
```

The anchor must be a built-in phase, the name must be unique (and not a built-in), and the named task (if any) must
exist - validated when the deploy starts. The task can branch on the phase via `{{.phase}}` / `$DEPLOY_PHASE`.
A plugin can add the same thing from its startup hook with `cfg.AddPhase(...)` - see
[Developing -> custom phases](/plugins/developing/#add-a-custom-phase).
