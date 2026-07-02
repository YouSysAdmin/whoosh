---
title: "Task state"
description: "Capture a task's stdout (json/text/lines) and read it from later tasks in the same run as {{ .tasks.<name> }}."
weight: 70
---

A task can publish its result for later tasks in the same run.
Set `output: json` (or `text` / `lines`), its combined stdout is captured, parsed, and stored under the task name -
readable anywhere templates work as `{{ .tasks.<name> }}`.

```yaml
tasks:
  whoami: # source task
    local: true
    output: json # json -> map/list/scalar, text -> string, lines -> []string
    cmds: [ aws sts get-caller-identity ]
  notify: # dest task (deps order it after the producer)
    deps: [ whoami ]
    envs: { ACCOUNT: '{{ .tasks.whoami.Account }}' }
    cmds: [ 'echo acct={{ .tasks.whoami.Account }}' ]
```

- **Ordering is yours** - the producer must run before the consumer (list it in `deps:` or earlier in a hook list).
- **Single target** - an `output:` task runs on one target (local, or the first matching host), since a multi-host
  value would be ambiguous.
- **Dashed names** - use `{{ index .tasks "fetch-info" "field" }}` (dot access like `.tasks.fetch-info` isn't valid
  template syntax).
- Reading unset state errors on a real run (typo guard), while `--dry-run` renders it as `<no value>`.
  Runnable example:
  [`examples/05-tasks-and-scripts`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/05-tasks-and-scripts).
