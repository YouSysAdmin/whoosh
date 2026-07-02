# 05 - tasks & scripts cookbook

A grab-bag of task and templating features, each in its own task.
Everything runs locally (`local: true`), so you can execute any of them on your machine.

## Files

```
Deployfile.yml               # one task per feature
app.env                      # dotenv file loaded via env_files
deploy/demo.yml              # a local host + one deploy:false (inventory-only) host
deploy/scripts/info.sh.tmpl  # a templated file script
```

## Run

```sh
whoosh demo --help                    # every task is a subcommand
whoosh demo show-context              # {{.x}} templates vs $X env vars
whoosh demo inventory                 # {{ range .config.hosts }}{{ .address }}{{ end }}
whoosh demo deploy:hosts              # the built-in host table (note archive = deploy:false)
TOKEN=s3cr3t whoosh demo with-secret  # {{ env "VAR" }} reads operator env
whoosh demo env-file                  # values from app.env (env_files), envs overriding
whoosh demo inline-script             # multi-line inline script, templated
whoosh demo file-script               # deploy/scripts/info.sh.tmpl
whoosh demo in-dir                    # explicit dir: + per-task env:
whoosh demo flaky                     # continue_on_error: true
whoosh demo pipeline                  # deps: [build] runs build first
whoosh demo use-state                 # reads JSON captured by the `fetch` task
```

## Concepts shown

| Task | Feature |
|---|---|
| `show-context` | `{{.app_name}}` etc. vs `$APP_NAME` - both forms in `cmds` |
| `inventory` | iterating hosts in a template: `{{ range .config.hosts }}{{ .address }}{{ end }}` |
| `with-secret` | `{{ env "TOKEN" }}` pulls a secret from the operator/CI env (and is redacted in output) |
| `env-file` | `env_files: [app.env]` loads a dotenv file into every task's env, beneath `envs` (which overrides) |
| `inline-script` | multi-line inline `script:` (always templated) |
| `file-script` | a `*.tmpl` file from `deploy/scripts/`, templated automatically, plus `{{.config}}` iteration |
| `in-dir` | explicit `dir:` and per-task `env:` (task env beats global env) |
| `flaky` | `continue_on_error: true` |
| `build` / `pipeline` | `silent:` and `deps:` |
| `fetch` / `use-state` | task state - `output: json` captures stdout, consumed as `{{ .tasks.fetch.field }}` (ordered via `deps`) |
| `deploy/demo.yml` | a `deploy: false` host stays in inventory but is never targeted |

## vars vs env

- **`vars`** - referenced in templates as `{{.KEY}}` *and* exported as `$KEY`. Any YAML type.
  Use for values you interpolate into commands.
- **`env`** - exported as `$KEY`, shell-expanded (so `$HOME`/`$PATH` work), and each value is itself templated (so `{{
  env "X" }}` reads the operator's environment). Use for `PATH`, tokens, and other shell environment.
