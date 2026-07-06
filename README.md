# Whoosh (formerly known as Deployer)

A single-binary deployment tool.
`whoosh` connects to your hosts over SSH, builds a timestamped release from git, links shared files/dirs into it,
atomically swaps the `current` symlink, and supports rollback - all driven by a `Deployfile.yaml` (or
`Whooshfile.yaml`) and per-stage `deploy/<stage>.yaml` files.

It runs from your machine or CI.
There is no agent or server to install on the targets (they just need `git`, `tar`, a POSIX shell and deps of your
scripts).

## Install / build

```sh
go build -o whoosh ./cmd/whoosh                 # the whoosh binary (no AWS plugins)
go build -tags noplugins -o whoosh ./cmd/whoosh # drop the bundled plugins
```

The AWS plugin links the AWS SDK v2 (~57 MB) and lives in its **own module**
(`github.com/yousysadmin/whoosh/plugins/aws`) - add it with a custom build (see below).
A `Deployfile` that references a plugin not built into the binary fails fast with
`unknown plugin "aws" (not built into this binary)`.

`make build` builds the binary, `make build-aws` builds one **with** the AWS plugin (via `whoosh build`), and `make
build-minimal` builds with no bundled plugins.

Run `whoosh plugins` (or `whoosh version`) to see which plugins a binary contains.

See [README.md](plugins/aws/README.md) for the Whoosh AWS Plugin

## Quick start

```sh
whoosh init                      # scaffold Whooshfile.yaml + whoosh/{staging,production}.yaml

# edit the files, point a stage at your hosts, then:
whoosh production deploy:check   # verify connectivity + create the dir tree
whoosh production deploy         # build + publish a release
whoosh production deploy:rollback
```

The command shape is **`whoosh <stage> <action>`**.

## Configuration

Shared config lives in `Deployfile.yaml`, per-stage hosts/overrides live in `deploy/<stage>.yaml` and are merged on
top (stage wins, vars merge by key).

All examples use `Deployfile.yaml` because this name is more familiar. :)

Instead of `Deployfile.yaml` and the `deploy` dir you can use any of the following filenames:

```
Whooshfile.yml
Whooshfile.yaml
whooshfile.yml
whooshfile.yaml
Whooshfile
Deployfile.yml
Deployfile.yaml
deployfile.yml
deployfile.yaml
Deployfile

// dotted
.Whooshfile.yml
.Whooshfile.yaml
.whooshfile.yml
.whooshfile.yaml
.Whooshfile
.Deployfile.yml
.Deployfile.yaml
.deployfile.yml
.deployfile.yaml
.Deployfile

// Stages dirs
whoosh
deploy

// dotted
.whoosh
.deploy
```

**Editor support (JSON Schema)**

whoosh ships a JSON Schema (`deployfile.schema.json`) generated from the config model.
Regenerate it after changing the config with `make schema`.

```yaml
# yaml-language-server: $schema=./deployfile.schema.json

# https://whoosh.yousysadmin.com/deployfile.schema.json
# https://yousysadmin.github.io/whoosh/deployfile.schema.json
# https://raw.githubusercontent.com/YouSysAdmin/whoosh/refs/heads/master/deployfile.schema.json  
```

> The schema validates the config structure and all core fields, but **not** plugin-specific `params:`/`with:` keys
> (e.g. the AWS plugin's options) - those live outside the core model and stay open (`additionalProperties`).

```yaml
# Deployfile.yaml
# yaml-language-server: $schema=https://whoosh.yousysadmin.com/deployfile.schema.json
version: "1"
app:
  name: myapp
  repo: git@github.com:org/myapp.git
  branch: main
  deploy_to: /var/www/myapp
  keep_releases: 5
linked_files: [ config/database.yaml, .env ]    # symlinked from shared/ - must exist there (checked)
linked_dirs: [ log, tmp/pids, public/uploads ] # symlinked from shared/ - created if missing
vars:
  RAILS_ENV: production
envs: # exported for every task command
  PATH: "$HOME/.rbenv/shims:$HOME/.rbenv/bin:$PATH" # rbenv/nvm shims, etc.
ssh:
  user: deploy
  port: 22
tasks:
  restart:
    desc: Restart the app
    roles: [ app ]
    cmds: [ "sudo systemctl restart {{.app_name}}" ]
  migrate:
    desc: Run migrations
    roles: [ db ]
    once: true # run on one host of the role
    cmds: [ "bin/rails db:migrate" ] # runs in the release dir by default
hooks:
  after:
    deploy:symlink: [ migrate ]
    deploy:publishing: [ restart ]
```

```yaml
# deploy/production.yaml
hosts:
  - address: web1.example.com
    roles: [ app, web ]
  - address: db1.example.com
    roles: [ db ]
```

### Sharing config between stages (`include:`)

When several stages need the *same* tasks/hooks/vars and only a few differ, factor the common parts into a fragment
and pull it in with `include:`.
It works like Ruby's `require_relative`: paths resolve **relative to the directory of the file that names them**,
includes nest (an included file can itself `include:` another), and the result is merged with the same rules as the
base<->stage merge.

```yaml
# deploy/production.yaml
include: shared/web.yaml # a string, or a list: [a.yaml, b.yaml]
hosts:
  - address: web1.example.com
    roles: [ app, web ]
```

```yaml
# deploy/shared/web.yaml
include: ../libs/proxy.yaml # climbs out of shared/ -> deploy/libs/proxy.yaml
vars:
  HEALTHCHECK: /up
tasks:
  restart:
    roles: [ app ]
    cmds: [ "sudo systemctl restart {{.app_name}}" ]
```

Precedence runs lowest -> highest: **`Deployfile.yaml` < the stage's includes (in listed order, each with its own
nested includes) < `deploy/<stage>.yaml`** - so a stage file overrides what it includes, which overrides the shared
`Deployfile.yaml`.
Maps (`vars`, `envs`, `tasks`, `hooks`) merge by key and `hosts`/`plugins` concatenate, exactly as in the base<->stage
merge. `include:` also works in `Deployfile.yaml` itself.
A missing or circular include is a hard error, and the key never appears in `whoosh <stage> config`.

### Local execution mode (no SSH)

Mark a host `local: true` to run the entire lifecycle on the current machine through the local shell - no SSH
connection, keys, or `known_hosts` needed. Useful for deploying on the target box itself, or for development.
Everything else (releases, shared symlinks, `current`, rollback, tasks, hooks) is identical.

```yaml
# deploy/local.yaml
hosts:
  - address: localhost
    local: true
    roles: [ app, web, db ]
```

```sh
whoosh local deploy
```

Local and remote hosts can coexist in one stage, each running over its own transport.

### Inventory vs deploy targets

By default, every host in a stage is a deployment target.
Set `deploy: false` to keep a host in the **inventory** - visible in `config` and host listings - without deploying
the app to it. Hosts with `deploy: false` are excluded from the deployment lifecycle, tasks, hooks, and ad-hoc `run`.
Only `config` and inventory-printing actions show them.

```yaml
hosts:
  - address: web1.example.com
    roles: [ app, web ]
  - address: bastion.example.com
    roles: [ ops ]
    deploy: false # listed, but never deployed to
```

This pairs with dynamic inventory: a plugin can discover a whole fleet and flag which hosts to deploy to (see the
`aws:ec2:inventory` `deploy_tag` below).

Two task flags change which hosts a task targets, relative to the default (deploy-enabled hosts):

- **`non_deploy: true`** - target *only* the `deploy:false` hosts (the inverse of normal targeting).
  For acting on hosts you don't deploy to, e.g. health checks ASG instances that boot from a baked AMI.
- **`all_hosts: true`** - target *every* host, ignoring the `deploy` flag (both `deploy:true` and `deploy:false`).
  For a task that should hit the whole fleet, e.g. collecting disk usage. (Wins over `non_deploy` if both are set.)

Roles and `--roles`/`--host` still narrow the set in all cases.

```yaml
tasks:
  asg-healthcheck: # only the deploy:false hosts
    non_deploy: true
    scripts: [ { path: healthcheck.sh } ]
  disk-usage: # every host in the stage
    all_hosts: true
    cmds: [ "df -h /" ]
```

> **Both flags act on the inventory captured at process start.**
> A fresh `whoosh <stage> <task>` run re-fetches dynamic inventory, so instances created *during* a deployment (e.g. by
> an
> ASG refresh) only appear on the **next** invocation - not to a hook running within the same deploy.
> So run these as their own post-deploy CI step (`whoosh prod asg-healthcheck`), not as a deployment hook.

### Unreachable hosts (`on_unreachable`)

By default, a host that becomes unreachable during a deployment aborts the whole run.
Set `on_unreachable: skip` to instead drop the unreachable host and finish on the survivors:

```yaml
on_unreachable: skip # abort (default) | skip
hosts:
  - address: db1.example.com
    roles: [ db ]
    required: true # never skip this one - its loss always aborts
  - address: web1.example.com
    roles: [ app, web ] # skippable under `skip`
  - address: web2.example.com
    roles: [ app, web ]
```

- **`abort`** (default) - any unreachable host fails the deployment. Unchanged behavior.
- **`skip`** - an unreachable host is dropped (from the remaining phases *and* from hook tasks), the deployment
  completes
  on the rest, the skipped hosts are logged, and the command exits **non-zero** so CI notices.
  Per-host `required: true` keeps a critical host from being skipped (the lock-holding primary is always required).
- Applies to **unreachable** hosts only - a dial failure or a connection lost mid-command.
  A command that *runs and exits non-zero* (e.g. a failed migration) always aborts regardless (use per-task
  `continue_on_error` to soften those).
- "Unreachable" is decided by the connection timeouts: ~15s to dial, and the ~30s SSH keepalive for a host that goes
  silent mid-command (see *SSH* below).

### Task fields

`desc`, `cmds`, `scripts`, `deps`, `dir`, `envs`, `silent`, plus deploy extras: `roles` (target hosts), `local` (run
on your machine), `once` (one host per role), `non_deploy` / `all_hosts` (target deploy:false / every host - above),
`strict_host_key` (override `ssh.strict_host_key` for this task), `continue_on_error`, `hidden`, `only`/`except`
(per-stage activation - below), `replace`, `output` (capture state - below).
A task's `cmds` run first, then its `scripts`.

- **`deps`** - list other task names. They run fully, in order, before this task (with cycle detection).
  This is the task -> task dependency mechanism, independent of hooks (which bind tasks to lifecycle *phases*).
  Note: deps run **every time** the task is invoked - there is no per-run de-duplication.
- **`hidden: true`** - omit the task from the CLI listing (`whoosh <stage> --help`).
  It is still runnable directly by name and still usable as a `deps` entry or a hook target - for helper tasks (a
  `setup` dep, a hook-only `restore-manifest`) that shouldn't clutter the command list.
- **`only` / `except`** - gate which stages the task is active in, the same filter plugins use (`only` lists the
  stages it runs in, empty = all, while `except` lists stages to skip, and `except` wins).
  A task inactive for the stage is **skipped** (logged), not run - whether invoked directly, as a `deps` entry, or
  from a hook - and is omitted from that stage's `--help` listing. So a shared hook like
  `deploy:published: [restart, warm-cache]` can run `restart` everywhere and skip `warm-cache` where `except:
[staging]`, without per-stage hook lists.
- **`replace: deploy:rollback`** - make this task run **in place of** a phase's built-in command.
  Only `deploy:rollback` is replaceable today: so an `aws:ec2:asg:rollback` (or any) task can take over `whoosh
  <stage> deploy:rollback` instead of the default current-symlink swap - one rollback command, app-specific behavior.
  The phase's `before`/`after` hooks still run, and `--cleanup` doesn't apply to a replaced rollback.
  (At most one task may replace a phase.)

  ```yaml
  rollback_asg:
    action: aws:ec2:asg:rollback
    replace: deploy:rollback # `whoosh <stage> deploy:rollback` now rolls the ASG
    with: { name: "{{ .asg_name }}" }
  ```

### Task state (`output:`)

A task can publish its result for other tasks in the same run to consume.
Set `output: json` (or `text` / `lines`) and the task's combined stdout is captured, parsed, and stored under the
task's name - readable anywhere templates work (`cmds`, `scripts`, `envs:`) as `{{ .tasks.<name> }}`:

```yaml
tasks:
  whoami: # producer
    local: true
    output: json # json -> map/list/scalar, text -> string, lines -> []string
    cmds: [ aws sts get-caller-identity ]
  notify: # consumer (deps order it after the producer)
    deps: [ whoami ]
    envs: { ACCOUNT: '{{ .tasks.whoami.Account }}' }
    cmds: [ 'echo acct={{ .tasks.whoami.Account }}' ]
```

- **Ordering** is yours: the producer must run before the consumer - list it in `deps:` (or earlier in a hook list).
  `deps` run fully, in order, first.
- **Single target**: an `output:` task runs on one target (local, or the first matching host) - a multi-host value
  would be ambiguous. Its `cmds`+`scripts` stdout is concatenated then parsed (for `json`, emit one document).
- **Dashed names**: use `{{ index .tasks "fetch-info" "field" }}` (dot access like `.tasks.fetch-info` isn't valid
  template syntax).
- Reading unset state is an error on a real run (typo guard).
  `--dry-run` renders it as `<no value>` since the producer hasn't run.
  Runnable example: [`examples/05-tasks-and-scripts`](examples/05-tasks-and-scripts/) (`fetch` / `use-state`).

### Environment and working directory

- **Working directory**: remote task/hook commands - and ad-hoc `run` - execute **inside the release directory** by
  default (the in-progress release during a deployment, the live `current` otherwise) - so `bundle install` / `rails
  db:migrate` find the `Gemfile`/app without a manual `cd`. Override per task with `dir:`.
  Local (`local: true`) tasks run in your machine's cwd.
  (A hook that runs *before* the release exists - before `deploy:updating` - must set its own `dir:`.)
- **Deploy context as env**: every task command, script, and ad-hoc `run` gets the deployment context exported as
  standard
  env vars - `$RELEASE_PATH`, `$CURRENT_PATH`, `$SHARED_PATH`, `$DEPLOY_TO`, `$RELEASE_TIMESTAMP`, `$COMMIT_HASH`,
  `$APP_NAME`, `$BRANCH`, `$STAGE`, `$REPO`, `$HOST` - plus your `vars`.
  So a `cmd` can use either `{{.release_path}}`/`{{.host}}` (Go template, expanded by whoosh) or
  `$RELEASE_PATH`/`$HOST` (shell env, expanded on the host). Both work in `cmds` and `scripts` alike.
- **Environment**: a top-level `envs:` map is exported for every task command, script, and ad-hoc `run`, and a task's
  own `envs:` overrides it per key.
  Values are **shell-expanded**, so they can reference existing vars - this is how you put language-version- manager
  shims on `PATH`:

  ```yaml
  envs:
    PATH: "$HOME/.rbenv/shims:$HOME/.rbenv/bin:$PATH" # rbenv (nvm/asdf likewise)
  ```

`envs` values are also **Go-templated**, so you can pull a value from whoosh's own (operator/CI) environment with
sprig's `env` helper - handy for secrets you don't want in the Deployfile, e.g. a private gem/npm registry credential:

  ```yaml
  envs:
    # value of $BUNDLE_RUBYGEMS__PKG__GITHUB__COM on the machine running whoosh
    BUNDLE_RUBYGEMS__PKG__GITHUB__COM: '{{ env "BUNDLE_RUBYGEMS__PKG__GITHUB__COM" }}'
  ```

(`envs` differs from `vars`: `vars` are template values, while `envs` is the shell environment for commands.
Note a value injected this way is visible in `--dry-run` output and the remote process list.)

For a **secret** that must not appear in logs, use `envSecret` instead of `env` (or wrap any value with `sensitive`):
it returns the value for use in the command but registers it so whoosh masks it everywhere - the echoed command,
output, dry-run plans, logs:

  ```yaml
  cmds:
    - bundle config set --global rubygems.pkg.github.com {{ envSecret "REG_TOKEN" }}
    # logs show: bundle config set --global rubygems.pkg.github.com [FILTERED]
  ```

- **Env files** (`env_files:`): load dotenv (`.env`) files into every task's environment as a base layer that `envs`
  overrides.
  Paths resolve against the Deployfile dir, later files win (a stage file's `env_files` are appended after the shared
  ones), a missing file is skipped, and the loaded values are kept out of `whoosh <stage> config`.
  The values are also visible to the `env`/`envSecret` template helpers - anywhere a template runs (`vars`, plugin
  `params`, `cmds`, ...), `{{ env "NAME" }}` reads whoosh's own environment first and falls back to the `env_files`
  value when the process var is unset (a set-but-empty process var still wins, the usual dotenv convention).
  Sprig's `expandenv` reads only the process environment - use `env`.

  ```yaml
  env_files:
    - .env
  vars:
    app_version: '{{ env "APP_VERSION" }}' # process env, else .env
  ```

### Scripts

A task can run shell scripts in addition to (or instead of) `cmds`:

```yaml
tasks:
  setup:
    roles: [ app ]
    envs:
      TOKEN: abc123
    scripts:
      - path: bootstrap.sh # from deploy/scripts/ (by name)
        interpreter: /usr/bin/env bash
      - path: /opt/ops/check.sh # absolute path, used as-is
      - name: inline-step # inline content (Go-templated like cmds)
        script: |
          echo "release {{.release_path}} on $HOST"
```

- **Lookup**: a bare name resolves under `deploy/scripts/` (override with top-level `scripts_dir:`), while an absolute
  path is used directly.
- **Interpreter**: defaults to `/bin/sh`. Set `interpreter` for `bash`, `python3`, etc.
- **Transport**: the script is read on your machine and streamed to the interpreter over stdin - so it works the same
  over SSH and in local mode, no upload needed.
- **Environment**: each script gets the task's `envs` and the deployment context exported as standard
  env vars - `$RELEASE_PATH`, `$CURRENT_PATH`, `$SHARED_PATH`, `$DEPLOY_TO`, `$RELEASE_TIMESTAMP`, `$COMMIT_HASH`,
  `$APP_NAME`, `$BRANCH`, `$STAGE`, `$REPO`, `$HOST`.
  Config `vars` are template-only - surface one explicitly with `envs: { NAME: "{{ .var }}" }`.
- **Templating**: inline scripts are always Go-templated, while a file script is templated when its path ends in
  `.tmpl` or it sets `template: true`.
  Templates see the deployment context (`{{.release_path}}`, `{{.host}}`, `{{.stage}}`, your vars) plus the **whole
  resolved Deployfile** under `{{.config}}` - so a script can build flexible logic from the config rather than
  hand-rolled bash:

  ```gotemplate
  {{range .config.hosts}}{{if has "web" .roles}}reload {{.address}}
  {{end}}{{end}}
  ```

`whoosh init` creates `deploy/scripts/` with an `example.sh`.

### Plugins

Plugins are compiled into the binary and self-contained: the core never reaches into a plugin.
You list the plugins you want under `plugins:`.
Each one validates its params on load (`Configure`) and registers what it contributes:

- a **startup hook** - runs at load and can append to `hosts:` (dynamic inventory), or
- one or more **actions** - invoked by name from a task or hook.

**Bundled** (in every binary, on by default - disable with `enabled: false`):

| Plugin              | What it does                                                                                                                              | Docs                                                   |
|---------------------|-------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------|
| `print-hosts-table` | Prints the resolved hosts table at deploy start and via `whoosh <stage> deploy:hosts`                                                     | [README](plugins/standard/print_hosts_table/README.md) |
| `systemd`           | `systemd:start/stop/restart/enable/disable/daemon-reload` actions run `systemctl` on the task's hosts, ad-hoc or hooked to a deploy phase | [README](plugins/standard/systemd/README.md)           |

**Separate modules** (compiled in with `whoosh build --with <module>`):

| Plugin  | What it does                                                                                         | Docs                              |
|---------|------------------------------------------------------------------------------------------------------|-----------------------------------|
| `aws`   | EC2 inventory, ASG refresh/rollback, AMI create/cleanup, SSM & Secrets Manager env files and imports | [README](plugins/aws/README.md)   |
| `slack` | Deploy start/success/failure notifications and the `slack:send` action                               | [README](plugins/slack/README.md) |
| `rbenv` | Installs rbenv + ruby-build and the app's Ruby versions on the hosts before the release goes live    | [README](plugins/rbenv/README.md) |

To write your own, copy [`plugins/plugin-template`](plugins/plugin-template/) - a compiling, tested stub exercising
the full plugin interface.

### Available variables

`cmds` and inline `scripts` are Go templates ([sprig](https://masterminds.github.io/sprig/) helpers included), and the
same values are exported to the shell as env vars - so you can write either `{{.release_path}}` (rendered by whoosh)
or `$RELEASE_PATH` (expanded on the host).
Both forms work in `cmds`, inline `scripts`, file scripts, and ad-hoc `run`.

| Value                                       | Template                 | Env var                 |
|---------------------------------------------|--------------------------|-------------------------|
| App name (`app.name`)                       | `{{.app_name}}`          | `$APP_NAME`             |
| Repo URL (`app.repo`)                       | `{{.repo}}`              | `$REPO`                 |
| Branch (`app.branch`)                       | `{{.branch}}`            | `$BRANCH`               |
| Stage name                                  | `{{.stage}}`             | `$STAGE`                |
| Deploy root (`app.deploy_to`)               | `{{.deploy_to}}`         | `$DEPLOY_TO`            |
| Releases dir (`<deploy_to>/releases`)       | `{{.releases_path}}`     | `$RELEASES_PATH`        |
| Shared dir (`<deploy_to>/shared`)           | `{{.shared_path}}`       | `$SHARED_PATH`          |
| Git mirror (`<deploy_to>/repo`)             | `{{.repo_path}}`         | `$REPO_PATH`            |
| Current symlink (`<deploy_to>/current`)     | `{{.current_path}}`      | `$CURRENT_PATH`         |
| This release dir                            | `{{.release_path}}`      | `$RELEASE_PATH`         |
| Release id (timestamp)                      | `{{.release_timestamp}}` | `$RELEASE_TIMESTAMP`    |
| Deployed commit SHA                         | `{{.commit_hash}}`       | `$COMMIT_HASH`          |
| Target host the command runs on             | `{{.host}}`              | `$HOST`                 |
| Roles of that host (its full set)           | `{{.roles}}` (list)      | `$ROLES` (comma-joined) |
| Deploy phase a hook is running for          | `{{.phase}}`             | `$DEPLOY_PHASE`         |
| Failure message (in a `deploy:failed` hook) | `{{.error}}`             | `$DEPLOY_ERROR`         |

Plus:

- **Your `vars`** - each key is a template value: `vars: { RAILS_ENV: production }` -> `{{.RAILS_ENV}}`.
  Var values are **themselves Go templates**, rendered once at config load - so
  `vars: { app_version: '{{ env "APP_VERSION" }}' }` resolves from whoosh's environment (or `env_files`, see below)
  before anything uses the var. The load-time context is static: the app/stage/path keys, sprig, and
  `env`/`envSecret`/`sensitive` - a var cannot reference another var, `{{.config}}`, plugin imports, or run-time
  values (`release_path`/`host`/... render empty at load). Template string args need **double quotes**
  (`{{ env 'X' }}` is a parse error), and a literal `{{` in a var value must be escaped as `{{ "{{" }}`.
- **`envs:` entries** (global and per-task) - exported as env vars (`$NAME`).
- **`{{.config}}`** (template only) - the whole resolved Deployfile keyed by its YAML field names, for flexible logic:
  `{{.config.app.name}}`, `{{range .config.hosts}}{{.address}} {{end}}`.
- **`{{.tasks.<name>}}`** (template only) - captured output of a task declaring `output:` (see [Task
  state](#task-state-output)) - e.g. `{{ .tasks.whoami.Account }}`.
- **Helper functions** in templates - the full [sprig](https://masterminds.github.io/sprig/) set
  (`{{ toJson .config.app }}`, `{{ join "," .roles }}`, `{{ .region | default "eu-west-1" }}`,
  `{{ env "CI_COMMIT_SHA" }}` reads whoosh's own environment falling back to `env_files` values,
  `{{ now | date "2006-01-02" }}`, ...) plus whoosh's
  own `toYaml`/`fromYaml`/`fromYamlArray` and `required "msg" .val` (fail the render when a value is nil/empty) -
  see [Templating & variables](https://whoosh.yousysadmin.com/configuration/templating/#helper-functions).

Notes: template keys are lowercase, env names are UPPERCASE.
During a deployment, `release_path`/`release_timestamp` point at the new release and `commit_hash` is the SHA being
deployed (resolved from the git mirror after `deploy:updating`).
For a standalone task run they fall back to `current_path`, an empty timestamp, and an empty commit hash.
`phase` is set only while a task runs as a hook (the phase it's attached to, e.g. `deploy:publishing` or
`deploy:failed`) and is empty otherwise. `error` is set only in a `deploy:failed` hook.
So one templated script can branch on the phase - see [`examples/06-slack-notify`](examples/06-slack-notify/).

The resolved host table is printed automatically at the start of every deploy by the default-on `print-hosts-table`
plugin, and on demand with `whoosh <stage> deploy:hosts`.
To list hosts from within a template, iterate `{{.config.hosts}}`:

```yaml
tasks:
  show-hosts:
    local: true # print once on your machine
    cmds: [ 'echo "hosts:{{ range .config.hosts }} {{ .address }}{{ end }}"' ]
```

## On-target layout

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

Each release records the deployed commit in `REVISION` (and the time in `REVISION_TIME`), and every deploy appends a
line to `revisions.log`: `Branch main (at <sha>) deployed as release <timestamp> by <user>`.

## Deploy phases (hook points)

```plain
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

Attach tasks with `hooks.before` / `hooks.after` keyed by phase name.
A hook task can read which phase it is running for via `{{.phase}}` / `$DEPLOY_PHASE`.

The **marker phases** - `deploy:init`, `deploy:started`, `deploy:updated`, `deploy:published`, `deploy:finished` - run
no built-in command. They are stable hook anchors marking a moment.
Prefer them for your own hooks: `deploy:init` to **provision the host** (install software / deps), `deploy:updated`
for *"the release is built but not yet live"*, and `deploy:published` for *"the release is live"* - without depending
on an internal step name like `symlink` / `publishing`.
(For a marker phase, `before` and `after` are the same point - use either.)

`deploy:init` fires right after the directory tree is ensured but before the release is built - the place to `apt-get
install`, ensure a runtime, etc. Because the release dir doesn't exist yet, set such a task's `dir:` to an existing
path (it's Go-templated, so `dir: "{{.deploy_to}}"` works):

```yaml
tasks:
  provision:
    desc: Install OS packages the app needs
    roles: [ app, web ]
    dir: "{{.deploy_to}}"            # release dir doesn't exist yet
    scripts:
      - path: provision.sh           # from deploy/scripts/
        interpreter: /usr/bin/env bash
hooks:
  before:
    deploy:init: [ provision ]
```

There is also a special **`deploy:failed`** hook key: its `after` tasks run when a deployment errors (after which the
deployment still fails), with the failure message exposed as `{{.error}}` / `$DEPLOY_ERROR` - useful for failure
notifications.
Because the phase is available, one task/script can handle start, success, and failure - see
[`examples/06-slack-notify`](examples/06-slack-notify/).
(For Slack specifically you don't need to script any of this: the
[slack plugin](plugins/slack/README.md) wires these notifications for you.)

`whoosh <stage> deploy:rollback` is a hook point too: `before` / `after` **`deploy:rollback`** tasks wrap the symlink
swap, and `after` tasks run with `current` already repointed at the restored release - whoosh's post-revert hook
point.
Use it to fix up shared state on rollback (e.g. restore a shared asset manifest) - see
[`examples/07-rails-assets`](examples/07-rails-assets/).

## Commands

| Command                                      | Description                                                               |
|----------------------------------------------|---------------------------------------------------------------------------|
| `whoosh init`                                | Scaffold config files                                                     |
| `whoosh <stage> deploy`                      | Build and publish a release                                               |
| `whoosh <stage> deploy:rollback [--cleanup]` | Repoint `current` at the previous release                                 |
| `whoosh <stage> deploy:check`                | Validate, ensure directory tree, verify `linked_files` exist in `shared/` |
| `whoosh <stage> deploy:unlock`               | Clear a stale deploy lock                                                 |
| `whoosh <stage> releases`                    | List releases per host                                                    |
| `whoosh <stage> <task>`                      | Run a Deployfile task                                                     |
| `whoosh <stage> run "<cmd>"`                 | Run an ad-hoc command                                                     |
| `whoosh <stage> deploy:hosts`                | Print the stage's hosts as a table (print-hosts-table plugin)             |
| `whoosh <stage> config`                      | Print the resolved, merged config                                         |
| `whoosh <stage> validate`                    | Validate the config                                                       |

Global flags: `--dry-run`, `--verbose/-v`, `--roles`, `--host/-H <host>`, `--concurrency <n>` (max hosts running a
command at once, `0` = all), `--deployfile <path>`.
`--dry-run` executes nothing and prints the plan at the same detail level as a live run's echo: task `cmds` in their
clean rendered form, scripts by name, built-in git/symlink commands omitted. Add `-v` to expand the plan to the full
commands that would be sent to each host (env exports, `cd`, script bodies, built-in steps).

Logging flags: `--log-level` (debug/info/warn/error, default info), `--log-format` (text/json, default text),
`--log-output` (stdout, stderr, or a file path), `--log-color` (on by default, automatically suppressed when output is
a file or a pipe, so logs stay clean).
Use `--log-file <path>` to **also** write a deployment log to a file alongside the console (`--log-file-format`, default
`text`). The file is never colorized.
With `text` the file is a **full transcript** - whoosh's narrative *and* the host command output (the raw remote/local
output that otherwise streams only to stdout), so you get a complete record of the deployment.
With `json` it's the narrative only, one JSON object per line (command output is excluded so the JSON stays valid).
Whoosh's progress (phases, tasks, results) is logged via `slog`, while remote command output streams separately,
prefixed by host - and the transcript captures both.
**Each task command is also echoed before it runs** (`[host] $ <command>`), so logs show what was sent to the host,
not just its output. (`cmds` always, but `scripts` and built-in git/symlink commands only under `--verbose`.)

**Secret redaction**: command output, the echoed commands, dry-run plans, and verbose command logs are scrubbed before
they reach the console or the transcript.
Two layers: (1) **built-in patterns** for well-known formats - AWS keys, GitHub/Slack/Stripe/SendGrid/Google/npm
tokens, JWTs, `key=secret` pairs, and credentials embedded in URLs (`https://user:TOKEN@host` ->
`https://user:[FILTERED]@host`).
(2) **user-marked secrets** for anything the patterns miss - `{{ envSecret "NAME" }}` (like sprig's `env` but always
redacted) and `{{ sensitive .value }}` register a value so its exact text is masked everywhere whoosh prints it.
Built-in redaction is pattern-based (best-effort), not a guarantee - for values that aren't a recognized format, mark
them with `envSecret`/`sensitive`.
Redaction is **disabled at `--log-level debug`**, so you can see raw output when debugging.

## Notes

- **Auth**: with no `ssh.identity_file` and no `ssh.identities`, whoosh uses your `ssh-agent` (`SSH_AUTH_SOCK`).
  When either is set, whoosh builds its own in-memory agent from those keys and the system agent is not consulted -
  so CI and multi-key setups need no `ssh-agent` on the operator machine:

  ```yaml
  ssh:
    identity_file: ~/.ssh/deploy       # joins the builtin agent
    identity_file_passphrase: '{{ envSecret "DEPLOY_KEY_PASS" }}'  # decrypts it when encrypted
    identities:                        # each entry is a key source, the name is just a label
      worker_hosts:
        path: ~/.ssh/id_worker         # a key file
      app_hosts:
        content: '{{ envSecret "APP_DEPLOY_KEY" }}'    # or the key PEM inline, e.g. from the env
        passphrase: '{{ envSecret "APP_KEY_PASS" }}'   # decrypts an encrypted key
      all_keys:
        path: ~/.ssh                   # or a directory: every key file in it is loaded
        recursive: true                # include subdirectories
  ```

  All keys are offered to every host (like a real agent), per-host `identity_file` overrides keep working - a host
  entry can also set its own `identity_file_passphrase` for an encrypted key (inherited from the global one only
  together with the global `identity_file`).
  A directory scan skips non-key files and encrypted keys it cannot open, while an explicit file or inline key that
  fails to load is a hard error. `content` and the passphrases are always redacted in the `config` dump and logs.
- **Host keys**: verified against `~/.ssh/known_hosts` by default, OpenSSH `accept-new` style: a host seen for the
  first time is trusted and its key appended to the known_hosts file (created, along with its directory, when missing),
  while a **changed** key fails - so fresh environments (containers, CI) work out of the box without losing
  protection against key swaps.
  Set `ssh.accept_new: false` to require every host key to already be present (strictest; pre-populate with
  `ssh-keyscan`), `ssh.strict_host_key: false` to skip verification entirely, or `ssh.known_hosts_file` for a custom
  path.
  A single task can override this with `strict_host_key: false` in its definition - for ephemeral hosts whose key is
  legitimately unknown (e.g.
  ASG instances from one AMI that share a key and rotate IPs), without loosening verification for the rest of the
  deployment.
- **Bastion (jump host)**: `ssh.bastion` routes every SSH connection through one jump host, like OpenSSH
  `ProxyJump` (single hop) - for app hosts that live in a private network:

  ```yaml
  ssh:
    user: deploy
    bastion:
      address: bastion.example.com
      user: jump                          # default: the operator's user
      identity_file: ~/.ssh/bastion_key   # default: builtin agent / ssh-agent
  ```

  The bastion connection is opened once, lazily on the first host dial, and shared - every host gets its own
  tunneled channel over it. The bastion authenticates like any host (its own `identity_file`, else the builtin
  agent, else your `ssh-agent` - it does not inherit `ssh.user`/`ssh.identity_file`) and its host key is
  verified with the same `strict_host_key`/`known_hosts_file`/`accept_new` settings as the targets.
  Agent forwarding applies to the app hosts only, never to the bastion itself (matching `ssh -J`).
  Local hosts bypass it, inventory-discovered hosts (e.g. private EC2 instances) are tunneled like any other
  host. The AWS plugin's `credentials_from_host` opens its own connection and is not tunneled.
- **Liveness / hangs**: a new connection times out after 15s (TCP + handshake).
  On an established connection whoosh sends an SSH keepalive every 10s and drops the host after 3 missed replies
  (~30s) - so a host that dies mid-deploy (power loss, network partition) surfaces as an error and fails the run fast
  instead of hanging on a blocked command.
  `Ctrl-C` / `SIGTERM` cancels cleanly: in-flight commands are signaled and the deployment lock is released (deferred
  cleanup runs), rather than the process being killed outright.
- **Agent forwarding (remote git auth)**: by default the `git` clone/fetch on a host uses that host's own keys.
  To let it authenticate with *your* credentials instead (e.g. a private GitHub repo), forward your agent or a single
  key:

  ```yaml
  ssh:
    forward_agent: true            # forward the builtin agent when active, else your local ssh-agent
    # forward_key: ~/.ssh/deploy   # OR forward just this key, in-memory (never written to the host)
  ```

`forward_key` takes precedence if both are set, and must be an unencrypted key (use `forward_agent` for
passphrase-protected keys). With `ssh.identities` configured, `forward_agent` forwards the builtin agent and needs no
`SSH_AUTH_SOCK`. Forwarding applies to all remote hosts in the run.
The request is best-effort (like `ssh -A`): if a host refuses it (`AllowAgentForwarding no` in its `sshd_config`) the
command still runs, but git there won't see your keys - enable agent forwarding on the host for forwarded git auth to
work.

- **Concurrency**: each phase runs on all target hosts in parallel, with a barrier between phases.
  Output is streamed prefixed by host.
- **Locking**: a deployment takes a lock on the primary host to block concurrent deploys.

## Custom-builds & writing plugins

whoosh ships with its own ("standard") plugins, and you can build a binary that adds your own - private or third-party
plugins. The built-in `whoosh build` command composes a custom binary. It needs the Go toolchain on `PATH`.

```sh
# `whoosh build` ships in the whoosh binary (go install github.com/yousysadmin/whoosh/cmd/whoosh@latest)

whoosh build \
  --with github.com/yousysadmin/whoosh/plugins/aws \
  --with github.com/acme/private-plugins@v1.2.0 \
  -o ./whoosh
```

The bundled AWS plugin is added the same way - `--with github.com/yousysadmin/whoosh/plugins/aws`.

> **Note:** fetching an *in-repo* plugin module (`plugins/aws`, `plugins/rbenv`, `plugins/slack`) at a specific `@version` requires a
> subdirectory tag (e.g. `plugins/aws/v1.1.0`) to exist in the whoosh repo. Until such tags are published, bundle them
> from a local checkout with `--replace` (exactly what `make build-aws` does). Third-party plugin repos are unaffected -
> their normal module tags work with `--with module@version`.

- `--with module[@version]` - a plugin module to include (repeatable).
- `--replace old=./local/path` - build a module from a local checkout (repeatable).
  This is also how you build against a local whoosh: `--replace github.com/yousysadmin/whoosh=.`.
- `--no-standard` - omit the bundled plugins. `--tags` - extra go build tags.
- `--whoosh-version` - the whoosh version to build against (default `latest`).

Private modules use your normal Go auth (`GOPRIVATE` + `~/.netrc` or SSH `insteadOf`).
Cross-compile by setting `GOOS`/`GOARCH`.

**Writing a plugin.**
A plugin is a small Go module that imports whoosh's public API (`github.com/yousysadmin/whoosh`) and registers itself
in `init()`.
The contract: implement `Configure` to register named actions and/or a startup hook (which can add tasks, hooks,
custom phases, vars and secrets to the deployment).
The full authoring guide is [**Writing plugins**](https://whoosh.yousysadmin.com/developing/writing-plugins/).
For copy-ready starting points see [`examples/plugins/hello`](examples/plugins/hello), the focused examples in
[`examples/plugins`](examples/plugins) (pipeline tasks, hooks, secrets/vars, custom phases), or `plugins/aws` for a
full-featured example.
