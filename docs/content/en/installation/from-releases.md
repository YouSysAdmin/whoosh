---
title: "From a GitHub release"
description: "Install a prebuilt whoosh binary (Linux amd64/arm64, macOS arm64) or pull the container image."
weight: 20
icon: download
---

Every [release](https://github.com/YouSysAdmin/whoosh/releases) publishes a prebuilt `.tar.gz` per platform plus a
`checksums.sha256` file - no toolchain needed.
Your target hosts still need `git`, `tar`, a POSIX shell, and SSH (see [Requirements](/installation/#requirements)).

Each release (`v{VERSION}`) ships these archives.

| Platform                             | AWS-free archive                        | AWS-enabled archive                         |
|--------------------------------------|-----------------------------------------|---------------------------------------------|
| Linux x86-64 - `linux_amd64`         | `whoosh_v{VERSION}_linux_amd64.tar.gz`  | `whoosh-aws_v{VERSION}_linux_amd64.tar.gz`  |
| Linux ARM64 - `linux_arm64`          | `whoosh_v{VERSION}_linux_arm64.tar.gz`  | `whoosh-aws_v{VERSION}_linux_arm64.tar.gz`  |
| macOS Apple Silicon - `darwin_arm64` | `whoosh_v{VERSION}_darwin_arm64.tar.gz` | `whoosh-aws_v{VERSION}_darwin_arm64.tar.gz` |

Intel macOS (`darwin_amd64`) isn't prebuilt - [build from source](../from-source/) there.
For any **other** plugin (or your own), compile a binary with `whoosh build` -
see [Need the AWS plugin?](#need-the-aws-plugin) below.

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

## Need the AWS plugin?

The AWS plugin (`aws:*` - EC2 inventory, ASG/AMI, SSM/Secrets) is prebuilt: download the **`whoosh-aws_*`** archive for
your platform (same steps as above, swapping the `whoosh_` prefix for `whoosh-aws_`) and you get a `whoosh` binary with
`aws:*` baked in - confirm with `whoosh plugins` (it lists `aws`).

For **any other** plugin (third-party or your own), or to combine several, compile a binary with `whoosh build`:

```sh
# `whoosh build` ships in every whoosh binary (go install github.com/yousysadmin/whoosh/cmd/whoosh@latest)
whoosh build --with github.com/yousysadmin/whoosh/plugins/aws -o ./whoosh
```

See [With custom plugins](../custom-plugins/) for details.
On an AWS-free binary, a `Deployfile` that references the `aws` plugin fails fast with `unknown plugin "aws" (not
built into this binary)`.

## Container image

Multi-arch images (amd64 + arm64) are published to GitHub Container Registry:

```sh
docker pull ghcr.io/yousysadmin/whoosh:latest        # or a specific tag, e.g. :1.0.0

# mount your project (the Deployfile lives in /work) and your SSH agent:
docker run --rm -it \
  -v "$PWD:/work" \
  -v "$SSH_AUTH_SOCK:/ssh-agent" -e SSH_AUTH_SOCK=/ssh-agent \
  ghcr.io/yousysadmin/whoosh:latest production deploy
```

The image is Alpine-based and includes `git`, `tar`, an SSH client, and CA certificates, so it works for both remote
deploys and [local mode](/configuration/hosts/#local-execution-mode). It packages the AWS-free binary.
For the `aws:*` features in a container, build an image `FROM` your own custom-built binary (see [With custom
plugins](../custom-plugins/)).

## Verify

```sh
whoosh version
whoosh plugins      # which plugins are compiled in
whoosh --help
```
