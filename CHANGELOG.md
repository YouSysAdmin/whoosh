# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
 - plugins: bundled default-on `systemd` plugin - `systemd:start`/`stop`/`restart`/`enable`/`disable`/`daemon-reload`
   actions run `systemctl` on the task's hosts (system and `--user` units, optional `sudo -n`, `daemon_reload`,
   `--now`, `--no-block`), usable ad-hoc via `action:`/`with:` or auto-wired to a deploy phase via the plugin's
   `actions:` params (`phase`/`when`/`roles`).
 - plugin SDK: `HostCommandRunner` - the command counterpart to `HostFileWriter`. The executor hands it to every
   action via ctx (`whoosh.HostCommandRunnerFrom`), so a plugin action can run a command on the hosts its task
   targets (parallel, fail-fast, echoed per host).

## [1.1.1] - 2026-07-03
### Changed
 - Allow work inside untrusted environments - Github Actions, GitLab, etc.
   By default, SSH `accept new` is set to `true`, which allows you to not have a valid `known_hosts` file  and it will be created and filled in during deploy.
   As before, host key checking can be completely disabled using `strict_host_key: false`.

   I recommend caching this file and mounting it before deployment if your infrastructure configuration is stable.

## [1.1.0] - 2026-07-03

#### Changed
 - deployfile.schema.json updated

#### Fixed
 - logs: small fixes for JSON log format
 - docs: fix internal links and typos

#### Added
 - plugins: Slack plugin imported into Whoosh

## [1.0.0] - 2026-07-03

First public release.
Version changed from 8.3.1 to v1.0.0 - the new era

[Unreleased]: https://github.com/YouSysAdmin/jc2aws/compare/v1.1.1...HEAD
[1.1.1]: https://github.com/YouSysAdmin/jc2aws/releases/tag/v1.1.1
[1.1.0]: https://github.com/YouSysAdmin/jc2aws/releases/tag/v1.1.0
[1.0.0]: https://github.com/YouSysAdmin/jc2aws/releases/tag/v1.0.0
