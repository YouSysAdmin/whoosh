---
title: "With custom plugins"
description: "Compose a custom whoosh binary that bundles your own private or third-party plugins with the whoosh build command."
weight: 30
icon: rocket
---

Plugins are compiled **into** the binary.
The default `whoosh` binary already ships all in-tree plugins (aws, slack, rbenv + the core set).
To bundle your **private or third-party** plugins - or compose a leaner set on top of the core - build
a custom binary with the **`whoosh build`** command: it starts from the core (whoosh's core plugins)
plus each module you name. Requires the **Go toolchain** on `PATH`.

The in-tree plugin modules are added the same way as any external plugin, e.g.
`--with github.com/yousysadmin/whoosh/plugins/aws`.

## Build

```sh
# `whoosh build` ships in every whoosh binary (go install github.com/yousysadmin/whoosh/cmd/whoosh-core@latest)
whoosh build \
  --with github.com/yousysadmin/whoosh/plugins/aws \
  --with github.com/acme/private-plugins@v1.2.0 \
  -o ./whoosh

./whoosh plugins        # lists the plugins compiled in
```

## Flags

| Flag                       | Meaning                                                                                                                                    |
|----------------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `--with module[@version]`  | A plugin module to include (repeatable).                                                                                                   |
| `--replace old=./path`     | Build a module from a local checkout (repeatable). Also how you build against a local whoosh: `--replace github.com/yousysadmin/whoosh=.`. |
| `-o, --output`             | Output binary path (default `whoosh`).                                                                                                     |
| `--tags`                   | Extra go build tags for the compile.                                                                                                       |
| `--whoosh-version`         | The whoosh version to build against (default `latest`).                                                                                    |

Building against local checkouts of both whoosh and your plugin:

```sh
whoosh build \
  --replace github.com/yousysadmin/whoosh=/path/to/whoosh \
  --with github.com/acme/myplugin \
  --replace github.com/acme/myplugin=/path/to/myplugin \
  -o ./whoosh
```

Private modules use your normal Go auth (`GOPRIVATE` + `~/.netrc` or SSH `insteadOf`), and you can cross-compile by
setting `GOOS`/`GOARCH`.

{{< callout type="note" title="Writing a plugin" >}}
For the plugin authoring contract - actions, startup hooks, custom phases, secrets, testing - see [Plugins -> Developing](/plugins/developing/).
{{< /callout >}}

## Verify

```sh
whoosh version
whoosh plugins # your plugins should be listed here
```
