---
title: "With custom plugins"
description: "Compose a custom whoosh binary that bundles your own private or third-party plugins with the whoosh build command."
weight: 30
icon: rocket
---

Plugins are compiled **into** the binary.
To add a plugin - whoosh's own **AWS plugin**, or your private/third-party ones - build a custom binary with the
**`whoosh build`** command: it bundles whoosh's standard plugins plus each module you name.
Requires the **Go toolchain** on `PATH`.

The AWS plugin is kept out of the default binary (it pulls the ~57 MB AWS SDK), so it's added the same way as any
external plugin: `--with github.com/yousysadmin/whoosh/plugins/aws`.

## Build

```sh
# `whoosh build` ships in the whoosh binary (go install github.com/yousysadmin/whoosh/cmd/whoosh@latest)
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
| `--no-standard` / `--tags` | Drop the bundled plugins / pass extra go build tags.                                                                                       |
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
For the plugin authoring contract - actions, startup hooks, custom phases, secrets, testing - see [Developing -> Writing plugins](/developing/writing-plugins/).
{{< /callout >}}

## Verify

```sh
whoosh version
whoosh plugins # your plugins should be listed here
```
