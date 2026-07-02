# Example plugins

A single Go module (`github.com/yousysadmin/whoosh-examples`) with a few small, focused plugins that demonstrate the
public whoosh plugin SDK (`github.com/yousysadmin/whoosh`).
Each sub-package is an independent plugin you can compile into a custom whoosh binary with [`whoosh
build`](../../internal/cli/builder).

| Package | Plugin name | Demonstrates |
|---|---|---|
| [`pipeline/`](pipeline) | `example-pipeline` | Add a task to the pipeline, run cmds + an embedded script on hosts, wire it into a phase with an after-hook |
| [`config/`](config) | `example-config` | Read a stage var, register a secret (`AddSecret`), inject a template/command value (`AddImport`), and an action that uses it |
| [`phase/`](phase) | `example-phase` | Define a **custom phase** inserted after a built-in phase (and usable as a hook anchor) |

For the minimal "hello world" plugin, see [`../plugin-hello`](hello).

## Build a whoosh that includes them

With the Go toolchain on `PATH`:

```sh
# against a tagged release:
whoosh build \
  --with github.com/yousysadmin/whoosh-examples/pipeline \
  --with github.com/yousysadmin/whoosh-examples/phase \
  -o ./whoosh

# against this checkout (no tag needed):
whoosh build \
  --replace github.com/yousysadmin/whoosh="$PWD/../.." \
  --with github.com/yousysadmin/whoosh-examples/pipeline \
  --replace github.com/yousysadmin/whoosh-examples="$PWD" \
  -o ./whoosh

./whoosh plugins          # lists the compiled-in plugins
```

## Writing your own

Copy any of these packages (or `../plugin-hello`) into your own module. A plugin:

1. imports only `github.com/yousysadmin/whoosh` (never whoosh's `internal/...`),
2. calls `whoosh.Register(name, factory)` in `init()`, and
3. implements `Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error` - registering actions (`reg.AddAction`)
   and/or a startup hook (`reg.AddStartup`) that mutates the resolved config (`cfg.AddTask`, `cfg.AddHookAfter`,
   `cfg.AddPhase`, `cfg.AddImport`, `whoosh.AddSecret`, ...).
