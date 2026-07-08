---
title: "From a GitHub release"
description: "Install a prebuilt whoosh binary (Linux amd64/arm64, macOS arm64) or pull the container image."
weight: 20
icon: download
---

Every [release](https://github.com/YouSysAdmin/whoosh/releases) publishes a prebuilt `.tar.gz` per platform plus a
`checksums.sha256` file - no toolchain needed.
Your target hosts still need `git`, `tar`, a POSIX shell, and SSH (see [Requirements](/installation/#requirements)).

Each release (`v{VERSION}`) ships two flavors per platform:

- **`whoosh`** - the default binary with all in-tree plugins (aws, slack, rbenv + the core set)
- **`whoosh-core`** - a light binary with only the core plugins

| Platform                             | Default archive (all plugins)           | Core archive (core plugins only)             |
|--------------------------------------|-----------------------------------------|----------------------------------------------|
| Linux x86-64 - `linux_amd64`         | `whoosh_v{VERSION}_linux_amd64.tar.gz`  | `whoosh-core_v{VERSION}_linux_amd64.tar.gz`  |
| Linux ARM64 - `linux_arm64`          | `whoosh_v{VERSION}_linux_arm64.tar.gz`  | `whoosh-core_v{VERSION}_linux_arm64.tar.gz`  |
| macOS Apple Silicon - `darwin_arm64` | `whoosh_v{VERSION}_darwin_arm64.tar.gz` | `whoosh-core_v{VERSION}_darwin_arm64.tar.gz` |

The binary inside both archives is named `whoosh` - confirm what a binary contains with `whoosh plugins`.
Intel macOS (`darwin_amd64`) isn't prebuilt - [build from source](../from-source/) there.

## Download and install

Set `VERSION` to the release (without the leading `v`) and `PLATFORM` to your OS/arch:

```sh
VERSION=1.0.0
PLATFORM=darwin_arm64   # or: linux_amd64 | linux_arm64

base="https://github.com/YouSysAdmin/whoosh/releases/download/v${VERSION}"
curl -fsSL -o whoosh.tar.gz "${base}/whoosh_v${VERSION}_${PLATFORM}.tar.gz"
curl -fsSL -O "${base}/checksums.sha256"

# verify the download (Linux: sha256sum - macOS: shasum -a 256)
sha256sum --ignore-missing -c checksums.sha256 2>/dev/null \
  || shasum -a 256 -c checksums.sha256 --ignore-missing

tar -xzf whoosh.tar.gz whoosh
install -m 0755 whoosh /usr/local/bin/whoosh   # or anywhere on $PATH
```

For the core flavor, swap the `whoosh_` prefix for `whoosh-core_` in the archive name.

## Homebrew

The default (all plugins) binary is published to the `YouSysAdmin/homebrew-apps` tap for macOS
(Apple Silicon) and Linux:

```sh
brew install yousysadmin/apps/whoosh
```

## Linux packages

Each release also attaches `deb`, `rpm`, and `apk` packages of the default (all plugins) binary,
named `whoosh_v{VERSION}_linux_<arch>.<format>`:

```sh
dpkg -i whoosh_v${VERSION}_linux_amd64.deb        # Debian/Ubuntu
rpm -i whoosh_v${VERSION}_linux_amd64.rpm         # RHEL/Fedora
apk add --allow-untrusted whoosh_v${VERSION}_linux_amd64.apk   # Alpine
```

## Need a smaller binary or a custom plugin set?

The `whoosh-core_*` archives skip the separate plugin modules (the AWS SDK alone is ~57 MB of source),
leaving only the core plugins - a good fit for simple deploys.

For a third-party or private plugin, or to combine a custom set on top of the core, compile a binary
with `whoosh build`:

```sh
# `whoosh build` ships in every whoosh binary
whoosh build --with github.com/acme/private-plugins@v1.2.0 -o ./whoosh
```

See [With custom plugins](../custom-plugins/) for details.
A `Deployfile` that references a plugin not built into the binary fails fast with `unknown plugin "aws" (not
built into this binary)`.

## Container image

Multi-arch images (amd64 + arm64) are published to GitHub Container Registry:

```sh
docker pull ghcr.io/yousysadmin/whoosh:latest        # all plugins - or a specific tag, e.g. :1.0.0
docker pull ghcr.io/yousysadmin/whoosh:latest-core   # core plugins only, e.g. :1.0.0-core

# mount your project (the Deployfile lives in /work) and your SSH agent:
docker run --rm -it \
  -v "$PWD:/work" \
  -v "$SSH_AUTH_SOCK:/ssh-agent" -e SSH_AUTH_SOCK=/ssh-agent \
  ghcr.io/yousysadmin/whoosh:latest production deploy
```

The image is Alpine-based and includes `git`, `tar`, an SSH client, and CA certificates, so it works for both remote
deploys and [local mode](/configuration/hosts/#local-execution-mode). `:latest` packages the default (all plugins)
binary, `:latest-core` the core one. For a custom plugin set in a container, build an image `FROM` your own
custom-built binary (see [With custom plugins](../custom-plugins/)).

## Verify

```sh
whoosh version
whoosh plugins      # which plugins are compiled in
whoosh --help
```
