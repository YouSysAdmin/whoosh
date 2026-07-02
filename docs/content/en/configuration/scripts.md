---
title: "Scripts"
description: "Run shell scripts from a task - by file name, by absolute path, or inline - streamed to the interpreter over stdin."
weight: 60
---

A task can run shell scripts in addition to or instead of `cmds`:

```yaml
tasks:
  setup:
    roles: [ app ]
    envs: { TOKEN: abc123 }
    scripts:
      - path: bootstrap.sh           # resolved from deploy/scripts/ by name
        interpreter: /usr/bin/env bash
      - path: /opt/ops/check.sh      # absolute path, used as-is
      - name: inline-step            # inline content (Go-templated)
        script: |
          echo "release {{.release_path}} on $HOST"
```

| Field         | Description                                                                                   |
|---------------|-----------------------------------------------------------------------------------------------|
| `path`        | A file by name (resolved under `scripts_dir`, default `deploy/scripts/`) or an absolute path. |
| `script`      | Inline script content (always Go-templated).                                                  |
| `name`        | Label for logs (defaults to the file's base name).                                            |
| `interpreter` | Interpreter to run under (default `/bin/sh`), e.g. `/usr/bin/env bash`, `python3`.            |
| `template`    | Render a *file* script as a Go template (files ending `.tmpl` are templated automatically).   |

Provide exactly one of `path` or `script`.
The content is read on your machine and **streamed to the interpreter over stdin** - so it works identically over SSH
and in local mode, with no upload.
Scripts get the task's `envs`, the config `vars`, and the deploy context exported as env vars (see [Templating &
variables](/configuration/templating/)).
