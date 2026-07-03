---
title: "From source"
description: "Build whoosh with the Go toolchain - go install or a checkout + make, with build tags to trim the binary."
weight: 10
icon: terminal
---

Build whoosh yourself with the Go toolchain. Requires **Go 1.26+**.

## go install

The quickest path - installs the latest tagged version:

```sh
go install github.com/yousysadmin/whoosh/cmd/whoosh@latest
```

It lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is unset) - make sure that's on your `PATH`.

## Clone and build

A checkout gives you the `Makefile` targets and the test suite:

```sh
git clone https://github.com/YouSysAdmin/whoosh.git
cd whoosh
go build -o whoosh ./cmd/whoosh          # or: make build  (output: dist/whoosh)
install -m 0755 whoosh /usr/local/bin/whoosh   # or anywhere on $PATH
```

Useful targets:

```sh
make build        # build dist/whoosh
make test         # go test ./...
make lint         # go vet + gofmt check
make help         # list all targets
```

## Build tags

The binary is small and **AWS-free** - `go build` bundles only the lightweight `print-hosts-table` plugin.
The AWS plugin lives in its own module and is added with a custom build, **not** a build tag (see [With custom
plugins](../custom-plugins/)). You can still drop the bundled plugins:

```sh
go build                 -o whoosh ./cmd/whoosh # the binary (default plugins only)
go build -tags noplugins -o whoosh ./cmd/whoosh # no bundled plugins at all

# equivalents via the Makefile:
make build           # the binary
make build-aws       # WITH the AWS plugins, via `whoosh build`
make build-minimal   # = -tags noplugins
```

A `Deployfile` that references a plugin not built into the binary fails fast with `unknown plugin "aws" (not built
into this binary)`.

{{< callout type="info" title="Need the AWS plugin (or your own)?" >}}
The AWS plugin and any private/third-party plugin are added with the `whoosh build` command, not a build tag - see
[With custom plugins](../custom-plugins/).
{{< /callout >}}

## Verify

```sh
whoosh version
whoosh plugins      # which plugins are compiled in
whoosh --help
```
