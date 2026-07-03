---
title: "aws"
description: "The AWS plugin - EC2 inventory, ASG refresh/rollback, AMI create/cleanup, SSM/Secrets Manager - plus credentials and writing your own."
weight: 10
---

Plugins are compiled into the `whoosh` binary and are self-contained - the core never reaches into a plugin.
You list the ones you want under `plugins:`. Each validates its config on load and registers what it contributes:

- a **startup hook** - runs at load and can append to `hosts:` (dynamic inventory), and/or
- one or more **actions** - invoked by name from a task or hook (`action: <name>`).

## Build with AWS plugin
```sh
whoosh build --with github.com/yousysadmin/whoosh/plugins/aws
```
(see [Installation -> With custom plugins](/installation/custom-plugins/)) - then list it under `plugins:`.

Listing it activates the plugin and all its features, which share one AWS connection (region + credentials) set in the
plugin's global `params`. Per-feature config goes under `actions:`.

```yaml
plugins:
  - name: aws
    params: # global: region + ONE credential source (shared)
      region: eu-west-1
      credentials_from_host: { host: "{{ .bastion }}", user: deploy }
    actions: # per-feature config (layered on the global params)
      - name: aws:ec2:inventory   # startup, listed because it needs tag filters
        params: { tags: { App: [ myapp ] } }
      - name: aws:ec2:asg             # actions are available even if not listed here
      - name: aws:ec2:ami
```

| Feature             | Kind              | Provides                                                                                                                                                                                                                                                                            |
|---------------------|-------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `aws:ec2:inventory` | startup           | Appends matching EC2 instances to `hosts:`. Runs **only when listed** under `actions:` (it needs tag filters).                                                                                                                                                                      |
| `aws:ec2:asg`       | actions           | `aws:ec2:asg:refresh` (start an instance refresh and wait) and `aws:ec2:asg:rollback` (roll the launch template back a version, then refresh). Available whenever the plugin is loaded.                                                                                             |
| `aws:ec2:ami`       | actions           | `aws:ec2:ami:create` (bake an AMI, optionally patch a launch template) and `aws:ec2:ami:cleanup` (prune old AMIs). Available whenever the plugin is loaded.                                                                                                                         |
| `aws:ssm`           | actions + startup | `aws:ssm:to-dotenv` (read SSM parameters by prefix and render a dotenv file on the task's hosts) - always available, and, when listed under `actions:` with `prefixes`, a startup hook that loads those parameters **once** into the template context as `{{ .ssm.* }}` / `$SSM_*`. |
| `aws:secrets`       | actions + startup | `aws:secrets:to-dotenv` and a startup hook - the Secrets Manager counterpart of `aws:ssm`.                                                                                                                                                                                          |

- **Credentials are global** - set them once in the plugin's `params`.
  Per-feature `params:` carry feature config (e.g. inventory tags), not credentials.
  The clients are built once, so there's a single bastion connection, not one per feature.
- **The asg/ami actions need no `actions:` entry** - adding the `aws` plugin makes them available, their behavior
  comes from each task's `with:`.
  `aws:ec2:inventory` is the exception: it's a startup that would otherwise query the whole account, so it runs only
  when listed.
- **A feature's `params:` are defaults for its actions.** Anything you put under a feature's `actions:` entry (e.g.
  `aws:ec2:asg`, `aws:ec2:ami`, `aws:ssm`, `aws:secrets`) is layered **under** each task's `with:` - the task wins,
  and nested maps (tags, source_tags, launch_template) merge by key.
  So you can set shared params once (e.g. the ASG `name`, used by both refresh and rollback) and override per task:

  ```yaml
  plugins:
    - name: aws
      actions:
        - name: aws:ec2:asg
          params: { name: "{{ .asg_name }}", instance_warmup: 120 }   # defaults
  tasks:
    refresh: { action: aws:ec2:asg:refresh }                          # uses the defaults
    refresh-fast:
      action: aws:ec2:asg:refresh
      with: { min_healthy_percentage: 50 }                            # adds to / overrides them
  ```

(Feature `params:` render with load-time context - `vars` + static config, a param that needs a run-time value like
`{{ .release_path }}` must go on the task `with:`.)

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

An action task runs once. `--dry-run` prints the call without contacting AWS.
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
The `print-hosts-table` plugin is one: it prints the resolved hosts table at the start of every deploy (after
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
activation](/configuration/tasks/#per-stage-activation-only--except).
Use it to scope a plain `cmds`/`scripts` task (one that doesn't depend on a plugin) to specific stages.
{{< /callout >}}

## Parameterizing plugins with vars

Plugin **`params:` (and per-action `params:`) are Go-templated** - rendered against the stage's `vars` plus the static
config (`{{.stage}}`, `{{.app_name}}`, ...) and sprig helpers (`{{ env "X" }}`).
Combined with the single `aws` plugin, this lets you keep the **logic in the shared `Deployfile.yml`** and change only
**vars per stage** - no duplicated plugin blocks:

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

[`examples/04-aws-inventory`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/04-aws-inventory) is built
entirely around this pattern - one `aws` plugin declared once, stages that differ only in their `vars`, and a
`staging` stage where `aws` is switched off.

## aws:ec2:inventory

A startup feature: it appends running EC2 instances matching your tag filters to `hosts:`, with the stage's SSH
defaults applied. Discovered hosts merge with any static ones you list.
Configure it under the plugin's `actions:` (it runs only when listed - credentials come from the plugin's global
`params`):

```yaml
plugins:
  - name: aws
    params:
      region: eu-west-1
      # ...credential params (see below)
    actions:
      - name: aws:ec2:inventory
        params:
          # tag filters: each value is a string or list (matches ANY listed value),
          # different keys are AND-ed together.
          tags:
            Environment: [ uat, staging ]
            App: myapp
          role_tag: deployer:roles    # optional: tag value (comma-separated) -> roles
          roles: [ app ]                # fallback roles when role_tag is absent/empty
          use_public_ip: false        # default: connect over the private IP
          # optional: only tag-matching instances are DEPLOYED to, the rest are
          # still listed (deploy: false) so you see the whole fleet but ship to a subset:
          deploy_tag: { Name: Deploy,   Value: "true" }
          # optional: tag-matching instances are required: true (never skipped
          # under on_unreachable: skip):
          required_tag: { Name: Critical, Value: "true" }
```

| Param           | Description                                                                                         |
|-----------------|-----------------------------------------------------------------------------------------------------|
| `tags`          | Tag filters, value is a scalar or list (any-of), keys AND-ed.                                       |
| `role_tag`      | A tag whose comma-separated value becomes the host's roles.                                         |
| `roles`         | Fallback roles when `role_tag` is absent.                                                           |
| `use_public_ip` | Use the public IP instead of the private one (default false).                                       |
| `deploy_tag`    | `{Name, Value}` - only instances with this tag deploy, others are inventory-only (`deploy: false`). |
| `required_tag`  | `{Name, Value}` - instances with this tag are `required: true`.                                     |

See the discovered fleet with `whoosh <stage> deploy:hosts` (provided by the default-on `print-hosts-table` plugin - it
works for any inventory source and shows `deploy: false` hosts).

## aws:ec2:asg:refresh

Starts an Auto Scaling Group instance refresh, then **polls until it finishes**, logging `status`/`percent` each
interval.
The task blocks until the rollout completes and fails if the refresh ends `Failed`/`Cancelled`/rolled-back, or if it
can no longer be found. A refresh already in progress is logged and skipped (not treated as an error).
There's no client-side timeout - AWS drives the refresh to a terminal state.
`Ctrl-C` stops the *waiting* (and says so), the refresh itself keeps running in AWS.

```yaml
tasks:
  refresh_asg:
    action: aws:ec2:asg:refresh
    with:
      name: my-asg                  # required: the ASG name
      min_healthy_percentage: 100   # default 100
      max_healthy_percentage: 200   # default 200
      instance_warmup: 300          # default 300 (seconds)
      skip_matching: true           # default true (skip instances already on the new LT)
      auto_rollback: false          # default false
```

## aws:ec2:asg:rollback

The **manual** counterpart to the instance refresh's `auto_rollback`.
The forward deploy (`aws:ec2:ami:create`) bumps the launch template to a new version with the new AMI.
This action reverses that by copying the **previous** launch template version forward to a new latest version, then
refreshing the group onto it.

```yaml
tasks:
  rollback_asg:
    action: aws:ec2:asg:rollback
    with:
      name: my-asg                  # required: the ASG to roll back and refresh
      # launch_template is OPTIONAL (default: the ASG's own template):
      # launch_template: { id: lt-0abc123 }   # a specific template, or
      # launch_template: { asg: other-asg }   # another ASG's template
      set_default: true             # default true: make the rolled-back copy the $Default
      # ...the same refresh preferences as aws:ec2:asg:refresh (min/max_healthy, etc.)
```

What it does, in order:

1. Resolve the launch template - explicit `launch_template.{id,asg}`, else the template attached to `name`.
2. List its versions and find the **previous** one (the second-highest version number - non-contiguous numbering from
   deleted versions is handled). Fewer than two versions is an error.
3. `CreateLaunchTemplateVersion` with `SourceVersion = <previous>` (no overrides), so the new **latest** version is an
   exact copy of the previous one.
4. If `set_default` (default true), make that new version the template's `$Default` - so a group launching from
   `$Default` picks it up (a group tracking `$Latest` does so regardless).
5. Start an instance refresh and **wait** for it (same polling/preferences as `aws:ec2:asg:refresh`).

| Param                                                                                                       | Description                                                                                |
|-------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|
| `name`                                                                                                      | The ASG to roll back and refresh (required).                                               |
| `launch_template`                                                                                           | `{id}` or `{asg}` - which template to roll back. Default: the template attached to `name`. |
| `set_default`                                                                                               | Make the rolled-back copy the `$Default` version (default true).                           |
| `min_healthy_percentage` / `max_healthy_percentage` / `instance_warmup` / `skip_matching` / `auto_rollback` | Refresh preferences, same as `aws:ec2:asg:refresh`.                                        |

{{< callout type="note" >}}
This works for groups whose launch template version is `$Default` or `$Latest` (the usual case, and what
`aws:ec2:ami:create` sets up).
A group pinned to a *specific* version number won't change what it launches - repin it instead.
{{< /callout >}}

## aws:ec2:ami:create

Bakes an AMI from a live instance and, **optionally**, points a launch template at it.

```yaml
tasks:
  bake_ami:
    action: aws:ec2:ami:create
    with:
      name_prefix: myapp                 # AMI named "<prefix>-<timestamp>"
      # source instance - set ONE (precedence: instance_id > source_tags > asg):
      asg: my-asg                        # first InService instance in this ASG
      # instance_id: i-0abc123           # a specific instance
      # source_tags: { Role: web }       # first running instance matching these tags
      # launch_template is OPTIONAL: omit to only bake, include to repoint one:
      launch_template: { asg: my-asg }   # this ASG's launch template, or:
      # launch_template: { id: lt-0abc123 }
      no_reboot: true                    # default true
      wait: true                         # default true, forced true when patching a launch template
```

| Param                                 | Description                                                                                                                                  |
|---------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `name_prefix`                         | The image is named `<prefix>-<timestamp>`.                                                                                                   |
| `instance_id` / `source_tags` / `asg` | Source instance, in that precedence order.                                                                                                   |
| `launch_template`                     | `{id}` or `{asg}` - if set, clone its `$Default` version onto the new AMI and make it default. Omit to leave all launch templates untouched. |
| `no_reboot`                           | Pass `NoReboot` to `CreateImage` (default true).                                                                                             |
| `wait`                                | Wait until the image is `available` (default true, forced when patching a launch template).                                                  |

It copies the source instance's tags onto the image (dropping `Name` and `aws:*`, setting `Name` to the AMI name) and
polls the image state until available (logging each check, so a long bake isn't mistaken for a hang).
The wait fails fast if the image is **deregistered or deleted mid-build** (it doesn't poll a doomed image to the
timeout), is bounded by a 30-minute cap, and on `Ctrl-C` reports the cancellation (not the timeout).

## aws:ec2:ami:cleanup

Deregisters old self-owned AMIs matching its filters and deletes their backing snapshots, keeping the newest
`keep_last`.

```yaml
tasks:
  prune_amis:
    action: aws:ec2:ami:cleanup
    with:
      name_prefix: myapp                 # Name starts with "<prefix>-"
      tags: # and/or tag filters (value scalar or list, AND-ed)
        Application: myapp
        Environment: production
      keep_last: 3                       # default 3
```

At least one filter (`name_prefix` or `tags`) is **required**, so a misconfiguration can't prune every image.

## aws:ssm:to-dotenv

Reads SSM Parameter Store parameters by prefix and renders them as a dotenv file **on the task's hosts** - for
materializing an app's `.env` from SSM during a deploy.
Parameters are fetched **once** (operator-side, with the plugin's credentials), then the file is written on every host
the task targets.

```yaml
tasks:
  get-env-from-ssm:
    desc: Render .env.local on the app hosts from SSM
    roles: [ app ]                       # the hosts to render the file on
    action: aws:ssm:to-dotenv
    with:
      prefixes:
        - "/my-app/prod/"              # trailing slash -> a tree (all params under it)
        - "/shared/github-auth-key"    # no slash       -> one parameter
      path: ".env.local"               # path on each host, relative -> the release dir
      # recursive: true                # default, walk the whole tree under each prefix
      # decrypt: true                  # default, decrypt SecureString values
      # full_key_path: false           # default, key = last path segment, else the full path
      # multiline: true                # default, keep real newlines in values (PEM keys, certs)
```

- **Prefix rule:** a prefix ending in `/` is fetched with `GetParametersByPath` (recursively), while one without a
  trailing slash is a single parameter fetched with `GetParameter`.
  (A missing single parameter is skipped, not an error.)
- Keys are derived from the last path segment (e.g.
  `/my-app/prod/DATABASE_URL` -> `DATABASE_URL`, `/shared/github-auth-key` -> `GITHUB_AUTH_KEY`) and normalized to
  dotenv form (uppercased, non-alphanumeric -> `_`). Set `full_key_path: true` to keep the whole path.
  Output is sorted and values are quoted/escaped.
- **Multiline values** (PEM keys, certs) keep their real newlines inside the quotes by default - the form the
  `dotenv`/Rails gems require:
  ```
  APP_TLS_CERTIFICATE="-----BEGIN CERTIFICATE-----
  ...
  -----END CERTIFICATE-----"
  ```

Set `multiline: false` to collapse newlines to a literal `\n` (one line per entry).

- The fetch happens **once** and the file is rendered on each host the task targets (set `roles:` to pick them), a
  relative `path` resolves against the **release dir**, an absolute one is used as-is.
  The file is created `0600` (it holds secrets).
  Run it from a hook (e.g. after `deploy:updated`) so the release dir exists.
  (If no executor host context is present - e.g. a standalone run with no matching hosts - it falls back to writing on
  the operator machine.)
- Needs `ssm:GetParametersByPath` and `ssm:GetParameter` (plus `kms:Decrypt` for SecureStrings) on the relevant
  parameter paths.

### Loading SSM parameters into the template context

Often you don't want a file - you want the parameters available to *every* task, command, and script.
List `aws:ssm` under `actions:` with `prefixes` and the plugin fetches them **once at startup** (operator-side, not
per host) and injects them into the template context:

```yaml
plugins:
  - name: aws
    params: { region: ca-central-1, credentials_from_host: { host: "{{ .bastion }}" } }
    actions:
      - name: aws:ssm
        params:
          prefixes:
            - "/my-app/prod/"            # trailing slash -> tree, none -> one param
            - "/shared/github-auth-key"
          # namespace: ssm     # default, the values land under this key
          # recursive: true  decrypt: true
```

Each parameter is keyed by its last path segment and exposed two ways:

| Parameter                 | Template                             | Env var                |
|---------------------------|--------------------------------------|------------------------|
| `/my-app/prod/secret`     | `{{ .ssm.secret }}`                  | `$SSM_SECRET`          |
| `/shared/github-auth-key` | `{{ index .ssm "github-auth-key" }}` | `$SSM_GITHUB_AUTH_KEY` |

So a task can use them directly, e.g. `cmds: [ "deploy --token=$SSM_GITHUB_AUTH_KEY" ]` or a templated config file.
The fetch happens once and is reused for every host's render.
Every value is **registered for masking** (masked in command echoes, output, and logs) and is held in a runtime-only
field, so it is **not** emitted by `whoosh <stage> config` nor visible under `{{.config}}`.
(Template field access needs a valid identifier - use `{{ index .ssm "has-dashes" }}` for keys with non-letters, the
env form is always normalized to `$SSM_...`.)

This is independent of `aws:ssm:to-dotenv`: list the startup to get context values, use the action to also write a
file - or both.

## aws:secrets:to-dotenv

Reads AWS **Secrets Manager** secrets by name/prefix and renders them as a dotenv file **on the task's hosts** - the
Secrets Manager counterpart of `aws:ssm:to-dotenv`.
Secrets are fetched **once** (operator-side, with the plugin's credentials), then the file is written on every host
the task targets.

```yaml
tasks:
  get-env-from-secrets:
    desc: Render .env.local on the app hosts from Secrets Manager
    roles: [ app ]                       # the hosts to render the file on
    action: aws:secrets:to-dotenv
    with:
      prefixes:
        - "my-app/prod/"               # trailing slash -> a set (every secret whose name starts with it)
        - "shared/github-auth-key"     # no slash       -> one secret
      path: ".env.local"               # path on each host, relative -> the release dir
      # json: <unset>                  # default auto-detect (see below), true = require object, false = never parse
      # full_key_path: false           # default, for single-value secrets, key = last name segment, else full name
      # multiline: true                # default, keep real newlines in values (PEM keys, certs)
```

- **Prefix rule:** a prefix ending in `/` lists every secret whose name **starts with** it (`ListSecrets`, paginated)
  and fetches each, one without a trailing slash is a single secret fetched with `GetSecretValue`.
  (A missing single secret is skipped, not an error.)
- **JSON values** - Secrets Manager secrets are commonly a JSON object holding many variables.
  By default each secret's value is **auto-detected**: if it parses as a JSON object it is expanded into one env var
  per key (e.g.
  `{"DATABASE_URL":"...","API_KEY":"..."}` -> `DATABASE_URL`, `API_KEY`), otherwise the whole value becomes a single
  var.
  Set `json: true` to *require* a JSON object (error otherwise) or `json: false` to never parse (one var per secret).
- For a **single-value** secret the key comes from the secret name's last segment (e.g.
  `shared/github-auth-key` -> `GITHUB_AUTH_KEY`), set `full_key_path: true` to keep the whole name.
  JSON-expanded keys are used as-is.
  All keys are normalized to dotenv form (uppercased, non-alphanumeric -> `_`), output is sorted and quoted/escaped.
- **Multiline values** (PEM keys, certs) keep their real newlines inside the quotes by default, set `multiline: false`
  to collapse to a literal `\n`.
- The fetch happens **once** and the file is rendered on each host the task targets (set `roles:` to pick them), a
  relative `path` resolves against the **release dir**, an absolute one is used as-is. The file is created `0600`.
  Run it from a hook (e.g. after `deploy:updated`) so the release dir exists.
  (With no executor host context it falls back to writing on the operator machine.)
- Needs `secretsmanager:GetSecretValue` and `secretsmanager:ListSecrets` (plus `kms:Decrypt` for secrets encrypted
  with a customer-managed key).

### Loading secrets into the template context

Like `aws:ssm`, list `aws:secrets` under `actions:` with `prefixes` to fetch them **once at startup** and inject them
into the template context (default namespace `secrets`):

```yaml
plugins:
  - name: aws
    params: { region: ca-central-1, credentials_from_host: { host: "{{ .bastion }}" } }
    actions:
      - name: aws:secrets
        params:
          prefixes:
            - "my-app/prod/"           # trailing slash -> set, none -> one secret
          # namespace: secrets         # default, the values land under this key
          # json: <unset>              # auto-detect JSON-object expansion
```

A JSON-object secret contributes one entry per key, while any other secret is keyed by its last name segment.
Both surfaces:

| Source                                         | Template                      | Env var                 |
|------------------------------------------------|-------------------------------|-------------------------|
| key `DATABASE_URL` in secret `my-app/prod/app` | `{{ .secrets.DATABASE_URL }}` | `$SECRETS_DATABASE_URL` |
| plain secret `my-app/prod/token`               | `{{ .secrets.token }}`        | `$SECRETS_TOKEN`        |

Every value is **registered for masking** and held in a runtime-only field, so it is **not** emitted by `whoosh
<stage> config` nor visible under `{{.config}}`.
(Use `{{ index .secrets "has-dashes" }}` for keys with non-letters, the env form is always normalized to
`$SECRETS_...`.)

## AWS credentials

These go in the `aws` plugin's **global `params:`** and are shared by every feature (one connection is built, not one
per feature). Set `region` (or let the standard AWS chain resolve it) and pick **one** credential source.
With none set, the SDK default chain is used (env vars, shared config/`profile`, and the local EC2 instance IAM role).

```yaml
params:
  region: eu-west-1
  profile: myprofile              # shared-config profile (AWS_PROFILE)

  # or static keys:
  access_key_id: AKIA...
  secret_access_key: ...
  session_token: ...              # optional

  # or a local YAML file of aws_* keys:
  credentials_file: /run/secrets/aws.yml

  # or fetch that YAML over HTTP (e.g. a private GitHub raw URL):
  credentials_url: https://raw.githubusercontent.com/org/repo/main/aws.yml
  credentials_token: ghp_...      # sent as "Authorization: token <token>"

  # or read temporary creds from a remote host's EC2 metadata (IMDSv2) over SSH:
  credentials_from_host:
    host: bastion.example.com
    user: deploy                  # optional (default: $USER)
    identity_file: ~/.ssh/id      # optional, ssh-agent is also used
    # port / strict_host_key / known_hosts_file also accepted
```

The file/URL YAML keys:

```yaml
aws_access_key_id: AKIA...
aws_secret_access_key: ...
aws_session_token: ...           # optional
aws_default_region: eu-west-1    # optional (also accepts aws_region)
```

**Precedence**: static keys -> `credentials_file` -> `credentials_url` -> `credentials_from_host`.
A region from the file/URL/host is used when `region:` is unset.
With no source set, the SDK default chain applies, `credentials_from_host` is specifically for reading a **remote**
instance's IAM role over SSH (e.g. when the operator has no local AWS creds but a box in the account does).

## Immutable-infra deploy chain

A typical bake -> roll -> prune wiring:

```yaml
hooks:
  after:
    deploy:published: [ bake_ami, refresh_asg, prune_amis ]
```

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
A plugin can add the same thing from its startup hook with `cfg.AddPhase(...)` - see [Writing
plugins](/developing/writing-plugins/#add-a-custom-phase).
