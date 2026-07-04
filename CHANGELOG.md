# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]
### Added
 - config `vars:` values are themselves Go templates, rendered once at config load against the static context
   (app/stage/paths, sprig, `env`/`envSecret`/`sensitive`) - so a var can pull from the environment:
   ```yaml
   env_files: [ ./dev.env ]
   vars:
     app_version: '{{ env "APP_VERSION" }}' # process env, else dev.env
   ```
   Limitations: a var cannot reference another var, `{{.config}}`, plugin imports, or run-time values
   (`release_path`/`host`/... render empty at load).
 - the `env`/`envSecret` template helpers now fall back to the `env_files` (dotenv) values when the process
   env var is unset (a set-but-empty process var still wins) - everywhere templates render: vars, plugin
   `params:`, `cmds`, scripts, `envs:`.

### Fixed
 - `whoosh <stage> config` now redacts registered secrets (e.g. `envSecret` values in vars or plugin params) in
   the dumped config, like every other output path. You can use `--log-level=debug` for show 'secrets' as plain text.
 - Configuration verification and validation process, now configuration validation works correctly for all phases.

## [1.3.0] - 2026-07-04
### Added
 - template helpers: `toYaml`, `fromYaml`, `fromYamlArray`, and `required "msg" .val` (fail the render when a
   value is nil/empty) - the gaps sprig doesn't cover. The full sprig set (`toJson`, `join`, `default`, ...) was
   already available in every template and is now documented in
   [Templating & variables](https://whoosh.yousysadmin.com/configuration/templating/#helper-functions).

### Changed
 - config `vars:` are no longer auto-exported as shell environment variables of task commands and scripts.
   This functionality was new and added with the aim of reducing the configuration volume,
   but it greatly increases the volume of commands transmitted over SSH connections
   and can cause silent conflicts between variables. 
   Such functionality should be investigated more carefully to prevent side effects.

   If you need to export a variable as an environment variable, you should use the old method at the global or task level:
   ```yaml
   vars:
     var: ""
   envs:
     VAR: "{{ .var }}"
   ```

### Fixed
 - `--dry-run` verbose and JSON log output

## [1.2.0] - 2026-07-04
### Added
 - plugins: bundled default-on `systemd` plugin - `systemd:start`/`stop`/`restart`/`enable`/`disable`/`daemon-reload`
   actions run `systemctl` on the task's hosts (system and `--user` units, optional `sudo -n`, `daemon_reload`,
   `--now`, `--no-block`), usable ad-hoc via `action:`/`with:` or auto-wired to a deploy phase via the plugin's
   `actions:` params (`phase`/`when`/`roles`).
 - plugin SDK: `HostCommandRunner` - the command counterpart to `HostFileWriter`. The executor hands it to every
   action via ctx (`whoosh.HostCommandRunnerFrom`), so a plugin action can run a command on the hosts its task
   targets (parallel, fail-fast, echoed per host).
 - Deployfile JSON Schema added to the docs
   ```
   https://whoosh.yousysadmin.com/deployfile.schema.json  
   https://yousysadmin.github.io/whoosh/deployfile.schema.json  
   https://raw.githubusercontent.com/YouSysAdmin/whoosh/refs/heads/master/deployfile.schema.json  
   ```

## [1.1.1] - 2026-07-03
### Changed
 - Allow work inside untrusted environments - Github Actions, GitLab, etc.
   By default, SSH `accept new` is set to `true`, which allows you to not have a valid `known_hosts` file  and it will be created and filled in during deploy.
   As before, host key checking can be completely disabled using `strict_host_key: false`.

   I recommend caching this file and mounting it before deployment if your infrastructure configuration is stable.

## [1.1.0] - 2026-07-03

### Changed
 - deployfile.schema.json updated

### Fixed
 - logs: small fixes for JSON log format
 - docs: fix internal links and typos

### Added
 - plugins: Slack plugin imported into Whoosh

## [1.0.0] - 2026-07-03

First public release.
Version changed from 8.3.1 to v1.0.0 - the new era

[Unreleased]: https://github.com/YouSysAdmin/whoosh/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.3.0
[1.2.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.2.0
[1.1.1]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.1
[1.1.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.0
[1.0.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.0.0
