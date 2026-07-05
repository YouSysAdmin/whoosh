---
title: "Overview"
description: "File layout, the two-file merge model, sharing config with include:, the top-level field index, the app block, and the merge rules."
weight: 10
---

Whoosh is configured by two YAML files per stage - a shared `Deployfile.yml` and a per-stage `deploy/<stage>.yml` that
is merged on top of it.

Scaffold both with:

```sh
whoosh init                       # writes Whooshfile.yml + whoosh/{staging,production}.yml + whoosh/scripts/example.sh
whoosh init --stages dev,qa,prod  # choose your own stages
```

`init` never overwrites an existing file (it prints `skip`).

## File layout

```
Deployfile.yml          # shared config
deploy/
  staging.yml           # stage: staging
  production.yml        # stage: production
  scripts/              # default location for task scripts referenced by name
    healthcheck.sh
```

{{< callout type="note" >}}
Both naming conventions are accepted: the shared file may be `Whooshfile.yml` **or** `Deployfile.yml` (plus
`.yaml`/dotted/extension-less variants), and the stage dir may be `whoosh/` **or** `deploy/` - discovery tries the
`Whooshfile`/`whoosh` spellings first. `whoosh init` scaffolds the `Whooshfile.yml` + `whoosh/` form, the docs use
`Deployfile.yml` + `deploy/` throughout, and everything applies to either.
{{< /callout >}}

A minimal pair:

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

## Sharing config between stages

When several stages need the *same* tasks/hooks/vars and only a few differ, factor the common parts into a fragment
file and pull it in with `include:`, instead of copy-pasting them into each stage file.

Paths resolve **relative to the directory of the file that names them**, and
includes **nest** - an included file can itself `include:` another.

```yaml
# deploy/production.yml
include: shared/web.yml          # a string, or a list: [a.yml, b.yml]
hosts:
  - address: web1.example.com
    roles: [ app, web ]
```

```yaml
# deploy/shared/web.yml
include: ../libs/proxy.yml       # climbs out of shared/ -> deploy/libs/proxy.yml
vars:
  HEALTHCHECK: /up
tasks:
  restart:
    roles: [ app ]
    cmds: [ "sudo systemctl restart {{.app_name}}" ]
```

`config/deploy` convention:

```
Deployfile.yml
deploy/
  production.yml        # include: shared/production.yml
  staging.yml           # include: shared/staging.yml
  shared/
    production.yml      # include: ../libs/proxy.yml,  prod-wide tasks/vars
    staging.yml         # staging-wide tasks/vars
  libs/
    proxy.yml           # reusable task library
```

**Precedence** runs lowest -> highest:

```
Deployfile.yml  <  a file's includes (in listed order, each with its own nested includes)  <  the file that includes them
```

So `deploy/<stage>.yml` overrides what it includes, which overrides the shared `Deployfile.yml`.
Each layer is combined with the same [merge rules](#merge-rules) as the base<->stage merge.

{{< callout type="note" >}}
`include:` also works in `Deployfile.yml` itself.
A **missing** or **circular** include is a hard error reported at load, and the `include:` key is resolved at load
time - it never appears in `whoosh <stage> config` or under `{{.config}}`.
Script names still resolve against the global `scripts_dir` (default `deploy/scripts`), not relative to the fragment
that references them.
{{< /callout >}}

## Top-level fields

| Field            | Type           | Description                                                                                                    |
|------------------|----------------|----------------------------------------------------------------------------------------------------------------|
| `version`        | string         | Schema version marker (use `"1"`).                                                                             |
| `include`        | string or list | Other config files to merge underneath this one - see [include](#sharing-config-between-stages).               |
| `app`            | map            | The application and where it lives, including `keep_releases` - see [app](#app).                               |
| `linked_files`   | list           | Files symlinked from `shared/` into every release - see [Linked files & dirs](/configuration/linked-files/).   |
| `linked_dirs`    | list           | Directories symlinked from `shared/` into every release.                                                       |
| `vars`           | map            | Template/env values - see [Vars & envs](/configuration/vars-and-envs/).                                        |
| `envs`           | map            | Shell environment exported to every command/script.                                                            |
| `env_files`      | list           | Dotenv files layered under `envs` - see [Vars & envs -> Env files](/configuration/vars-and-envs/#env-files).   |
| `ssh`            | map            | Connection defaults - see [Hosts -> SSH](/configuration/hosts/#ssh-connection-settings).                       |
| `hosts`          | list           | Deploy targets - see [Hosts](/configuration/hosts/). Usually in the stage file.                                |
| `tasks`          | map            | Named units of work - see [Tasks](/configuration/tasks/).                                                      |
| `hooks`          | map            | Tasks wired to lifecycle phases - see [Hooks & phases](/configuration/hooks/).                                 |
| `custom_phases`  | list           | Named phases spliced into the lifecycle - see [Plugins -> custom phases](/plugins/overview/#custom-phases).    |
| `scripts_dir`    | string         | Override the directory task `scripts:` resolve names against (default `deploy/scripts`).                       |
| `on_unreachable` | string         | `abort` (default) or `skip` - see [Unreachable hosts](/configuration/hosts/#unreachable-hosts-on_unreachable). |
| `plugins`        | list           | Plugins to load - see [Plugins](/plugins/overview/).                                                           |
| `log`            | map            | Logging config (level/format/output/color/file) - see [Logging & secret masking](/usage/#logging--secret-masking). |

### app

```yaml
app:
  name: myapp                          # used in logs, {{.app_name}} / $APP_NAME
  repo: git@github.com:org/myapp.git   # git remote (required to deploy)
  branch: main                         # branch to deploy (default: master)
  deploy_to: /var/www/myapp            # deploy root on each host (required)
  keep_releases: 5                     # releases kept per host, older pruned (default 5)
```

`deploy_to` is the root of the [on-target layout](/usage/#on-target-layout): `releases/`, `shared/`, `repo/`, and the
`current` symlink all live under it. `keep_releases` is how many timestamped releases to retain on each host, older
ones are pruned at `deploy:finishing`. It is exposed to templates/scripts as `{{.keep_releases}}` / `$KEEP_RELEASES`.

## Merge rules

`Deployfile.yml` (base) and `deploy/<stage>.yml` (override) are merged like this:

| Field                                                                                      | Behavior                                                                                                                            |
|--------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| Scalars (`version`, `scripts_dir`, `on_unreachable`), `app` (incl. `keep_releases`), `ssh` | Override wins per field.                                                                                                            |
| `vars`, `envs`, `tasks`, `hooks`                                                           | Merged **by key** (override wins for a duplicate key, and for a duplicate hook phase the whole list is replaced, not concatenated). |
| `hosts`, `plugins`                                                                         | **Concatenated** (base entries + stage entries).                                                                                    |
| `linked_files`, `linked_dirs`                                                              | A non-empty override **replaces** the base list wholesale.                                                                          |

So a stage file typically lists `hosts` and overrides a few scalars (`branch`, `deploy_to`) and `vars`, while the
shared file holds `tasks`, `hooks`, and defaults.

Files pulled in with [`include:`](#sharing-config-between-stages) are combined with these same rules, layered
**below** the file that names them: `Deployfile.yml < a file's includes < the file itself`.

## Editor support (JSON Schema)

Whoosh ships a JSON Schema (`deployfile.schema.json`) generated from the config model.
Regenerate it after upgrading whoosh (or changing the model) with `make schema` (or `go run ./cmd/gen-schema`).

Point your editor at it with a modeline on the **first line** of each config file:

```yaml
# yaml-language-server: $schema=./deployfile.schema.json
version: "1"
```

```yaml
# yaml-language-server: $schema=https://whoosh.yousysadmin.com/deployfile.schema.json

# yaml-language-server: $schema=https://yousysadmin.github.io/whoosh/deployfile.schema.json

# yaml-language-server: $schema=https://raw.githubusercontent.com/YouSysAdmin/whoosh/refs/heads/master/deployfile.schema.json
```

**VS Code** needs the Red Hat *YAML* extension.
Instead of per-file modelines you can map the files once in `settings.json`:

```json
{
  "yaml.schemas": {
    "./deployfile.schema.json": [
      "Deployfile.y*ml",
      "deploy/*.y*ml"
    ]
  }
}
```

**JetBrains IDEs** - *Settings -> Languages & Frameworks -> Schemas and DTDs -> JSON Schema Mappings*: add
`deployfile.schema.json` and map `Deployfile.yml`, `Deployfile.yaml`, and `deploy/*.yml`.
(The modeline above also works.)

{{< callout type="note" >}}
The schema validates the config structure and all core fields, but **not** plugin-specific `params:`/`with:` keys
(e.g. the AWS plugin's options) - those live outside the core model and stay open.
{{< /callout >}}
