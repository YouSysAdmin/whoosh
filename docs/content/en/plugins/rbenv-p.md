---
title: "rbenv"
description: "The RBENV plugin - install rbenv and specific Ruby versions."
weight: 20
---

Installs and wires up [rbenv](https://github.com/rbenv/rbenv) (+ [ruby-build](https://github.com/rbenv/ruby-build))
on the deploy hosts, ensures the Ruby versions your app needs are built, and makes
rbenv available to every subsequent whoosh task — all before the new release goes live.

The plugin ships in the default `whoosh` binary. If you use `whoosh-core`, add it to a custom build:
```sh
whoosh build --with github.com/yousysadmin/whoosh/plugins/rbenv
```
(see [Installation -> With custom plugins](/installation/custom-plugins/)) - then list it under `plugins:`.

## What it does

Listing the plugin is enough. Its startup hook contributes a task (`rbenv:setup`)
and auto-wires it to run **before the `deploy:updated` phase** — you don't add
anything to `hooks:`. On each run, on every targeted host, it:

1. **Checks whether rbenv is installed** — under `root` (`$HOME/.rbenv` by default)
   or already on `PATH` — and, if not, git-clones rbenv into `root`.
2. **Installs the builder** — clones ruby-build into `<root>/plugins/ruby-build`
   so `rbenv install` works (skipped when `install_ruby: false`).
3. **Registers rbenv** in the shell rc files (`.bashrc`, `.zshrc`, …) via a
   marker-guarded, idempotent `rbenv init` block.
4. **Ensures the desired Ruby versions** are installed — the union of the
   `versions:` param and any `.ruby-version` file (read on the operator at load
   time, and from the previous release on the host). With `prune: true`, versions
   **not** in that set are uninstalled (`global` is always kept). Optionally runs
   `rbenv global`.
5. **Injects rbenv into whoosh's env context** — prepends `<root>/bin` and
   `<root>/shims` to `PATH` and exports `RBENV_ROOT` for every later task, so
   `bundle`, `rails`, and `rake` resolve the rbenv Ruby with no extra config.
   Also exposed to templates as `{{ .rbenv.root }}`.

Everything is idempotent, so leaving the plugin listed across deploys is cheap.

## Requirements on the host

- `git` (to clone rbenv / ruby-build).
- To build Ruby: the usual ruby-build toolchain — a C compiler and the
  openssl / readline / zlib development headers.

  The plugin does not install system packages, see the official ruby-build [wiki](https://github.com/rbenv/ruby-build/wiki#suggested-build-environment).

## Configuration

```yaml
plugins:
  - name: rbenv
    params:
      root: "$HOME/.rbenv"                 # install dir, "~" expands to $HOME (default $HOME/.rbenv)
      versions: ["3.3.0", "3.2.2"]         # Ruby versions to ensure (union with .ruby-version)
      global: "3.3.0"                      # optional: rbenv global (always kept from pruning)
      prune: false                         # uninstall Ruby versions not in the desired set (opt-in)
      read_ruby_version: true              # also derive versions from .ruby-version (default true)
      ruby_version_file: ".ruby-version"   # operator-side path, relative to the Deployfile dir
      update: false                        # git pull rbenv/ruby-build when already present
      install_ruby: true                   # false = install only rbenv, no Ruby builds
      shells: ["bash", "zsh"]              # shell rc files to register rbenv init in
      roles: ["app"]                       # restrict to these roles (default: all deployable hosts)
      inject_path: true                    # prepend rbenv bin+shims to whoosh's PATH (default true)
      build_env:                           # extra env for ruby-build at compile time
        RUBY_CONFIGURE_OPTS: "--with-jemalloc --enable-yjit"
        MAKEOPTS: "-j 1"
      default_gems:                        # gems installed into every Ruby (rbenv-default-gems)
        - bundler
        - bcat ~>0.6
        - rails --pre
      plugins:                             # extra rbenv plugins to git-clone
        - { name: rbenv-vars, repo: https://github.com/rbenv/rbenv-vars.git, version: v1.2.0 }
      rbenv_repo: "https://github.com/rbenv/rbenv.git"
      ruby_build_repo: "https://github.com/rbenv/ruby-build.git"
      default_gems_repo: "https://github.com/rbenv/rbenv-default-gems.git"
      # Advanced: move the setup hook.
      task_name: "rbenv:setup"             # name of the contributed task
      phase: "deploy:updated"              # phase to anchor the hook to
      when: "before"                       # "before" (default) or "after"
```

### How Ruby versions are resolved

The desired set is the **union** of:

- `versions:` (explicit),
- `global:` (when set),
- the operator-side `.ruby-version` (read at load from `ruby_version_file`,
  relative to the Deployfile directory) — this is the version of the app you are
  deploying, and
- the previous release's `.ruby-version` on each host
  (`$CURRENT_PATH/.ruby-version`).

The setup runs before the release is published, so on the host it only sees the
*previous* release's `.ruby-version` (via the `current` symlink) — the version of
the app **being deployed** is picked up **operator-side**: run whoosh from your app
repo (or point `ruby_version_file` at it). Set `read_ruby_version: false` to use
only the explicit `versions:`/`global:`.

### Ruby build options

`build_env` is exported for the setup task, so ruby-build sees it when it compiles
Ruby — e.g. `RUBY_CONFIGURE_OPTS: "--with-jemalloc --enable-yjit"` or
`MAKEOPTS: "-j 1"`. Values are shell-expanded on the host, so `"-j $(nproc)"`
works. The plugin's own `RBENV_*` control variables always take precedence over
`build_env`.

### Default gems

When `default_gems` is set, the plugin installs the
[rbenv-default-gems](https://github.com/rbenv/rbenv-default-gems) plugin and writes
the list to `$RBENV_ROOT/default-gems` (one entry per line), so **every** Ruby built
by `rbenv install` automatically gets those gems. Each entry is a gem name optionally
followed by a version requirement or gem options, matching the default-gems file
format:

```yaml
default_gems:
  - bundler          # latest
  - bcat ~>0.6       # version requirement
  - rails --pre      # gem options
```

The setup runs before the Ruby-install step, so the list applies to versions built
in the same run. Leave `default_gems` unset to skip the plugin entirely. Override the
plugin source with `default_gems_repo`.

### Extra rbenv plugins

`plugins` git-clones additional rbenv plugins into `$RBENV_ROOT/plugins/<name>` (the
same place ruby-build and rbenv-default-gems live). Each entry needs a `name` (the
plugin dir) and a `repo` URL, and may pin a `version` — a tag, branch, or commit SHA:

```yaml
plugins:
  - { name: rbenv-vars,      repo: https://github.com/rbenv/rbenv-vars.git,   version: v1.2.0 }
  - { name: rbenv-update,    repo: https://github.com/rkh/rbenv-update.git,   version: 1961fa180280bb50b64cbbffe6a5df7cf70f5e50 }
  - { name: rbenv-installer, repo: https://github.com/rbenv/rbenv-installer.git } # latest
```

With a `version`, the plugin is fully cloned and checked out at that ref (so any commit
SHA is reachable), without one it is a shallow clone of the default branch. It is
idempotent: on a re-run a pinned plugin is fetched and re-checked-out at its `version`,
and an unpinned one is `git pull`ed only when `update: true`. Each is cloned before the
Ruby-install step so install-time plugins take effect. ruby-build and rbenv-default-gems
are managed by their own pinned options and need not be listed here.

## Running it standalone

The contributed task is a normal task, so you can run it outside a `deploy:` phases.

```sh
whoosh <stage> rbenv:setup
```
