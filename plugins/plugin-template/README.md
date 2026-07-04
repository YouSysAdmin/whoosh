# Whoosh Plugin Template

A scaffold for a whoosh plugin: **every SDK extension point** (`github.com/yousysadmin/whoosh`), implemented as
working, tested code with `TODO` markers where your logic goes. Copy the directory, rename things, delete the
features you don't need - each lives in its own file - and start developing.

## Layout - one extension point per file

| File           | Extension point                                                                                                                                                     |
|----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `plugin.go`    | `Register` (note on `RegisterDefault`), `Versioner`, `Configure` - decode → validate offline → register everything                                                    |
| `params.go`    | The three config surfaces (`params:` / `actions[].params` / `with:`) and the `merge`/`decodeFeature` layering helpers                                                 |
| `client.go`    | The placeholder for the external system you talk to - built once in `Configure`, shared by all features. **Start your rework here**                                   |
| `actions.go`   | Two `ActionFunc`s: `plugin-template:render` (fetch once → `HostFileWriter` writes on the task's hosts) and `plugin-template:exec` (`HostCommandRunner` runs on them)  |
| `inventory.go` | Startup feature `plugin-template:inventory` - dynamic inventory: discover hosts, stamp `Roles`/`User`/`Deploy`/`Required`/`Source`, append to `cfg.Hosts`             |
| `context.go`   | Startup feature `plugin-template:context` - fetch values once, `cfg.AddImport` them as `{{ .ns.key }}` / `$NS_KEY`, `whoosh.AddSecret` each                           |
| `setup.go`     | Startup feature `plugin-template:setup` - contribute a host-side script task (`cfg.AddTask` + embedded `templates/setup.sh`), wired via `AddHookBefore/After`, or a spliced `AddPhase` custom phase, plus `AddHookFuncBefore/After` func-hooks |
| `commands.go`  | `Commander` - the `whoosh <stage> plugin-template:status` CLI subcommand                                                                                              |
| `plugin_test.go` | SSH-free tests for all of the above: offline validation, fake host bridges for the actions, startup assertions. Keep it green while you replace the TODOs           |

Conventions the scaffold already follows (keep them):

- **Namespacing**: `pluginName` must prefix every action/feature name (segment before the first `:`) - the executor's
  per-stage skip logic depends on it.
- **Validate offline**: everything checkable without network happens in `Configure`/the `decode*Params` funcs, so
  `whoosh validate` catches bad config without connecting anywhere.
- **Actions are operator-side**: they run once, not per host, the two ctx bridges (`HostFileWriterFrom`,
  `HostCommandRunnerFrom`) are the only way to reach the task's hosts - and they are nil outside the executor, so the
  scaffold fails with clear errors instead of skipping silently.
- **Secrets**: anything sensitive is `whoosh.AddSecret`-ed the moment it exists (the global token at `Configure`,
  fetched values in `context.go`/`actions.go`), so whoosh masks it in every echoed command, output line, log, and
  dry-run plan.
- **Narrative vs output**: plugin narrative goes through `slog`, action progress goes to the `out` writer, nothing
  goes to `os.Stdout`.

## Start your own plugin

```sh
cp -r plugins/plugin-template /path/to/my-plugin && cd /path/to/my-plugin
```

1. **go.mod** - set your module path (e.g. `github.com/acme/whoosh-myplugin`), delete the `replace` line, and
   `require` a tagged whoosh version.
2. **Rename** - the package, `pluginName`/`pluginVersion` in `plugin.go`, and the feature/action consts.
3. **client.go** - replace the placeholder with your real client (`newClient`, `fetch`, `discover`).
4. **Trim** - delete the feature files you don't need and their registrations in `Configure` (and their tests).
5. **Test** - `go test ./...` stays SSH-free thanks to the fakes in `plugin_test.go`.
6. **Build a binary that includes it**:

```sh
whoosh build --with github.com/acme/whoosh-myplugin -o ./whoosh
# or against local checkouts:
whoosh build \
  --with github.com/acme/whoosh-myplugin \
  --replace github.com/acme/whoosh-myplugin=/path/to/my-plugin \
  -o ./whoosh

./whoosh plugins        # lists the compiled-in plugins
```

To ship a plugin **inside the whoosh binary by default** instead (a "standard" plugin), move the package under
`plugins/standard/<name>` in the whoosh repo (root module - drop the `go.mod`), register with
`whoosh.RegisterDefault`, and blank-import it from `plugins/standard/standard.go`.

## The full config surface (as-is)

```yaml
plugins:
  - name: plugin-template
    params:                                # global -> client.go
      endpoint: "https://api.example.com"
      token: '{{ env "EXAMPLE_TOKEN" }}'   # masked everywhere via whoosh.AddSecret
    actions:
      - name: plugin-template:inventory    # opt-in: discover hosts
        params: { roles: [app], user: deploy }
      - name: plugin-template:context      # opt-in: {{ .template.db_url }} / $TEMPLATE_DB_URL
        params: { keys: [db_url, api_key] }
      - name: plugin-template:setup        # opt-in: host-side setup task in the lifecycle
        params: { phase: "deploy:updated", when: "before", roles: [app] }
        # params: { custom_phase: "template:warmup" }   # ...or as a spliced custom phase
      - name: plugin-template:render       # optional defaults for the render action
        params: { path: ".env.generated" }

tasks:
  generate-env:
    roles: [app]
    action: plugin-template:render         # with: > actions: entry > params:
    with: { key: db_url }
  warmup:
    roles: [app]
    action: plugin-template:exec
    with: { cmd: "curl -fsS http://localhost:8080/healthz" }
```

CLI: `whoosh <stage> plugin-template:status` prints what the plugin contributes for the stage.

Docs: the full authoring guide is
[whoosh.yousysadmin.com/plugins/developing](https://whoosh.yousysadmin.com/plugins/developing/).
