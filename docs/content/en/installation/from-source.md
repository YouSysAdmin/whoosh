---
title: "From source"
description: "Build whoosh with the Go toolchain - go install or a checkout + make."
weight: 10
icon: terminal
---

Build whoosh yourself with the Go toolchain. Requires **Go 1.26+**.

## go install

The quickest path - installs the latest tagged version of the **core** flavor (core plugins only):

```sh
go install github.com/yousysadmin/whoosh/cmd/whoosh-core@latest
```

It lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is unset) - make sure that's on your `PATH`.

The default (all plugins) flavor can't be `go install`ed - its module pins the plugin modules to the
local checkout. Get it from a [release](../from-releases/) or build it from a clone (below).

## Clone and build

A checkout gives you the `Makefile` targets and the test suite:

```sh
git clone https://github.com/YouSysAdmin/whoosh.git
cd whoosh
make build                                     # the default binary, all plugins (output: dist/whoosh)
install -m 0755 dist/whoosh /usr/local/bin/whoosh   # or anywhere on $PATH
```

Useful targets:

```sh
make build        # build dist/whoosh (all plugins, from the cmd/whoosh module)
make build-core   # build dist/whoosh-core (core plugins only)
make test         # go test ./... (root + plugin modules)
make lint         # go vet + gofmt check
make help         # list all targets
```

The core binary is small - `go build ./cmd/whoosh-core` bundles only the core plugins
(`print-hosts-table`, `systemd`). A `Deployfile` that references a plugin not built into the binary
fails fast with `unknown plugin "aws" (not built into this binary)`.

{{< callout type="info" title="Need a private or third-party plugin?" >}}
The in-tree plugin modules ship in the default binary (`make build`). Any private/third-party plugin
is added with the `whoosh build` command - see [With custom plugins](../custom-plugins/).
{{< /callout >}}

## Verify

```sh
whoosh version
whoosh plugins      # which plugins are compiled in
whoosh --help
```
