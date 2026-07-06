---
title: "Vars & envs"
description: "vars (template values) vs envs (the shell environment), and how to mark secrets sensitive so they're redacted everywhere."
weight: 40
---

Both inject values into your commands, but they are different mechanisms:

- **`vars`** - template values.
  Each key is available as `{{.KEY}}` (Go template, rendered by whoosh). Vars are **not** exported to the shell.
  Var values are **themselves templates**, rendered once at config load, so a var can pull from whoosh's environment
  (or `env_files`, below): `app_version: '{{ env "APP_VERSION" }}'`. The load-time context is static - app/stage/path
  keys, sprig, `env`/`envSecret`/`sensitive` - a var cannot reference another var, `{{.config}}`, plugin imports, or
  run-time values (`release_path`/`host`/... render empty at load). Template string args need **double quotes**
  (`{{ env 'X' }}` is a parse error); escape a literal `{{` as `{{ "{{" }}`.
- **`envs`** - the shell environment for commands.
  Values are **shell-expanded** (so they can reference `$HOME`/`$PATH`) *and* **Go-templated** (so they can pull from
  vars with `{{ .var }}` or from whoosh's own environment with `env`).

```yaml
vars:
  RAILS_ENV: production # -> {{.RAILS_ENV}} in templates
  app_version: '{{ env "APP_VERSION" }}' # rendered once at load: process env, else env_files

envs:
  # surface a var to the shell explicitly:
  RAILS_ENV: "{{ .RAILS_ENV }}"
  # rbenv/nvm/asdf shims on PATH for the non-login SSH shell:
  PATH: "$HOME/.rbenv/shims:$HOME/.rbenv/bin:$PATH"
  # a secret pulled from the operator's / CI environment at run time:
  BUNDLE_GITHUB__COM: '{{ env "BUNDLE_GITHUB__COM" }}'
```

A top-level `envs:` applies to every command, script, and ad-hoc `run`, a task's own `envs:` overrides it per key.

## Env files

`env_files` loads dotenv (`.env`) files into the environment of every task, as a base layer **beneath** `envs` (an
explicit `envs:` entry overrides a file value):

```yaml
env_files:
  - .env            # KEY=value pairs, loaded for every task
```

- Paths resolve against the Deployfile's directory (an absolute path works too).
- A **missing file is skipped** silently, while a malformed one is an error.
- Files layer in listed order - **later entries win** - and a stage file's `env_files` are appended after the shared
  ones, so `deploy/<stage>.yml` can add to (or override) the shared set per stage.
- The loaded values are **not** emitted by `whoosh <stage> config`, so `.env` secrets stay out of the dumped config.
  They are *not* auto-masked in command output, though - use `envSecret`/`sensitive` (below) for values that must be
  hidden.
- The values are also visible to the `env`/`envSecret` **template helpers**: anywhere a template renders (`vars`,
  plugin `params`, `cmds`, ...), `{{ env "NAME" }}` reads whoosh's own environment first and falls back to the
  `env_files` value when the process var is unset (a set-but-empty process var still wins - the usual dotenv
  convention). Sprig's `expandenv` reads only the process environment - use `env`.
- In **task-time templates** (`cmds`, `scripts`, task `envs`, `dir`, `with`) the resolved global `envs:` values sit
  between the two, so the full lookup order is: process env > global `envs` > `env_files`. Global env values
  themselves render **without** that layer (they cannot reference each other - only the process env and `env_files`),
  and load-time templates (`vars`, plugin `params`) keep the plain process > `env_files` lookup.

{{< callout type="info" >}}
A value injected with `{{ env "X" }}` is visible in `--dry-run` output and the remote process list - it's for
convenience, not a secrets vault.
{{< /callout >}}

## Secrets in templates

Whoosh echoes each command before running it and redacts known secret formats, but a value the patterns don't
recognize would still print.
To force masking, mark it sensitive - the value is used in the command but shown as `[FILTERED]` everywhere (echo,
output, dry-run, logs):

| Helper                   | Use                                           |
|--------------------------|-----------------------------------------------|
| `{{ envSecret "NAME" }}` | Like `env`, but the value is always redacted. |
| `{{ sensitive .value }}` | Mark any var/expression sensitive.            |

```yaml
cmds:
  - bundle config set --global rubygems.pkg.github.com {{ envSecret "REG_TOKEN" }}
```

See [Usage -> Logging & secret masking](/usage/#logging--secret-masking) for the full masking model (built-in
patterns + user-marked secrets).
