---
title: "Templating & variables"
description: "Go templates + sprig in commands and scripts, plus the deploy context exported to the shell as $ENV vars - with the full variable table."
weight: 90
---

`cmds` and inline `scripts` are Go templates (with [sprig](https://masterminds.github.io/sprig/) helpers), and the same
values are exported to the shell
as env vars - so write either `{{.release_path}}` (rendered by whoosh) or`$RELEASE_PATH` (expanded on the host).

Both forms work in `cmds`, inline `scripts`, file scripts, and ad-hoc `run`.

| Value                                        | Template                 | Env var                 |
|----------------------------------------------|--------------------------|-------------------------|
| App name (`app.name`)                        | `{{.app_name}}`          | `$APP_NAME`             |
| Repo URL (`app.repo`)                        | `{{.repo}}`              | `$REPO`                 |
| Branch (`app.branch`)                        | `{{.branch}}`            | `$BRANCH`               |
| Stage name                                   | `{{.stage}}`             | `$STAGE`                |
| Deploy root (`app.deploy_to`)                | `{{.deploy_to}}`         | `$DEPLOY_TO`            |
| Releases dir                                 | `{{.releases_path}}`     | `$RELEASES_PATH`        |
| Shared dir                                   | `{{.shared_path}}`       | `$SHARED_PATH`          |
| Git mirror                                   | `{{.repo_path}}`         | `$REPO_PATH`            |
| Current symlink                              | `{{.current_path}}`      | `$CURRENT_PATH`         |
| This release dir                             | `{{.release_path}}`      | `$RELEASE_PATH`         |
| Release id (timestamp)                       | `{{.release_timestamp}}` | `$RELEASE_TIMESTAMP`    |
| Deployed commit SHA                          | `{{.commit_hash}}`       | `$COMMIT_HASH`          |
| Releases kept per host (`app.keep_releases`) | `{{.keep_releases}}`     | `$KEEP_RELEASES`        |
| Target host the command runs on              | `{{.host}}`              | `$HOST`                 |
| Roles of that host (its full set)            | `{{.roles}}` (list)      | `$ROLES` (comma-joined) |
| Deploy phase a hook is running for           | `{{.phase}}`             | `$DEPLOY_PHASE`         |
| Failure message (in a `deploy:failed` hook)  | `{{.error}}`             | `$DEPLOY_ERROR`         |

Plus:

- **Your `vars`** - each key is a template value (`{{.KEY}}`). To surface one to the shell, map it explicitly:
  `envs: { KEY: "{{ .KEY }}" }`. Var values are themselves templates, rendered once at load - see
  [Vars & envs](/configuration/vars-and-envs/).
- **`envs:` entries** (global and per-task) - exported as env vars.
- **`{{.config}}`** (template only) - the whole resolved Deployfile keyed by its YAML field names:
  `{{.config.app.name}}`, `{{range .config.hosts}}{{.address}} {{end}}`.
- **The host table** is printed by the `whoosh <stage> deploy:hosts` command (and auto-printed at the start of a
  deploy by the default `print-hosts-table` plugin).
  To iterate hosts in a template, use `{{ range .config.hosts }}{{ .address }}{{ end }}`.
- **`{{.tasks.<name>}}`** (template only) - captured [task state](/configuration/task-state/).
- **Helper functions** - the full sprig set plus whoosh's own, see below.

## Helper functions

Every template renders with the complete [sprig](https://masterminds.github.io/sprig/) function library (~100
helpers). Some of the most useful ones:

| Helper                                    | Example                                             |
|-------------------------------------------|-----------------------------------------------------|
| `toJson` / `toPrettyJson` / `fromJson`    | `{{ toJson .config.app }}`                          |
| `join` / `splitList`                      | `{{ join "," .roles }}`                             |
| `default` / `coalesce` / `ternary`        | `{{ .region \| default "eu-west-1" }}`              |
| `upper` / `lower` / `trim` / `replace`    | `{{ .app_name \| upper }}`                          |
| `b64enc` / `b64dec`                       | `{{ .docker_auth \| b64enc }}`                      |
| `env` (falls back to global `envs:` at task time, then `env_files` values) | `{{ env "CI_COMMIT_SHA" }}`       |
| `now` / `date`                            | `{{ now \| date "2006-01-02" }}`                    |

Whoosh adds the gaps sprig doesn't cover:

| Helper                       | Use                                                                                    |
|------------------------------|----------------------------------------------------------------------------------------|
| `{{ toYaml .v }}`            | Render any value as YAML (no trailing newline).                                        |
| `{{ fromYaml .s }}`          | Parse a YAML mapping - `{{ (fromYaml .tasks.info).version }}`.                         |
| `{{ fromYamlArray .s }}`     | Parse a YAML sequence - `{{ range fromYamlArray .s }}...{{ end }}`.                    |
| `{{ required "msg" .v }}`    | Fail the render with `msg` when the value is nil or empty (undefined keys already error). |

Plus the secret-marking helpers `envSecret`/`sensitive` - see
[Vars & envs -> Secrets in templates](/configuration/vars-and-envs/#secrets-in-templates).

{{< callout type="note" >}}
Template keys are lowercase, env names UPPERCASE.
During a deploy, `release_path`/`release_timestamp` point at the new release and `commit_hash` is the SHA being
deployed (resolved after `deploy:updating`).
For a standalone task run they fall back to `current_path`, an empty timestamp, and an empty commit hash.
`phase`/`error` are set only while a task runs as a hook.
{{< /callout >}}

{{< callout type="warning" title="Quote templated YAML values" >}}
A YAML value starting with `{` is read as a flow mapping, so `name: {{ .x }}` is a parse error.
Always quote a leading template: `name: "{{ .x }}"`.
{{< /callout >}}
