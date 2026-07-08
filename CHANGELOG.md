# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [1.6.0] - 2026-07-08
### Changed
 - Release binaries:
   - whoosh - contains all plugins
   - whoosh-core - only core plugins (print-host and systemd at the moment) and recommended for build your own binary

### Added
 - New deploy-context keys:
   - `{{.deployer}}` / `$DEPLOYER` - who runs whoosh: the `DEPLOYER` env var, else `git config user.name`,
     else `$USER`, else `unknown`. Also used for the deploy lock info and `revisions.log`.
   - `{{.previous_commit_hash}}` / `$PREVIOUS_COMMIT_HASH` - the SHA the live release was deployed from, read from
     `<current>/REVISION` on the primary host at deploy start (empty on a fresh deploy and outside a deploy).
   - `{{.changelog}}` / `$DEPLOY_CHANGELOG` - the commits between the previous and the new revision, captured from
     the repo mirror at `deploy:updating`: one per line as `<sha>|<author>|<email>|<subject>`, newest first, no
     merges, capped at 100. Empty before `deploy:updating`, on a fresh deploy, and when the revisions match.
     Capture failures (e.g. a force-push removed the previous SHA) warn and never fail the deploy.
 - Plugin SDK: `HostCommandCapturer` (`whoosh.HostCommandCapturerFrom`) - an action can capture command output from
   the first host its task targets, the capture counterpart to `HostCommandRunner`.
 - `sensitiveEnv` template helper - an alias of `envSecret`, named for symmetry with `sensitive`.
 - Slack plugin (`1.1.0`):
   - I spied the idea of the Slack notification format in one of our project, it took a lot of changes,
     but they are all useful in one way or another :)
   - `color_start` / `color_success` / `color_fail` / `color_rollback` params - per-event attachment-color overrides.
   - `rich_fields: true` - structured success/fail message with User, Stage, Branch, Revision, Duration, and
     Release (path) fields.
   - `changelog:` - post the core `{{.changelog}}` commit list on the success notification: linked commit subjects,
     author names, optional Slack `@mentions` via an email-to-member-ID `authors:` map, batched at Slack's 20-attachments-per-message limit.
     An unchanged redeploy posts an explicit "No changes since the previous release" note.
     Best-effort - never fails the deploy.
   - `deployer_github_lookup: true` - resolve a login-shaped deployer (e.g. `GITHUB_ACTOR`) to their GitHub display name
     in the rich User field.
   - Releases RPM/DEB/APK packages and Brew formula

### Fixed
 - Verbose logging: 
   - The `--verbose` flag now correctly shows the full compiled command actually sent to each host.
   - Debug run `--log-level=debug` also shows full commands.
     Secrets masking is disabled during debugging, so they are displayed without editing - keep this in mind if you are 
     running debug in an environment where you do not control logging.
     
     ```log
     [10.0.0.2] $ export APP_NAME="app"; export BRANCH="master";
                  export COMMIT_HASH=""; export CURRENT_PATH="/srv/app/current";
                  export DEPLOY_ERROR=""; export DEPLOY_PHASE="";
                  export RELEASE_PATH="/srv/app/releases/20260707074649";
                  ...
                  cd '/srv/app/releases/20260707074649' && bundle config set --global rubygems.pkg.github.com corp:[FILTERED]
     ```

 - The `env`/`envSecret` template helpers now correctly resolve global `envs:` values in task-time templates
   (`cmds`, scripts, task `envs:`, `dir:`, action `with:`).
   Lookup order: process env > global `envs:` -> `env_files` (a set-but-empty entry wins over the next layer, the usual dotenv convention).
   Global env values themselves still render against only the process env and `env_files`, so they cannot reference each other.
   Load-time templates (`vars:`, plugin `params:`) keep the plain process -> `env_files` lookup.

   ```yaml
   envs:
     RAILS_ENV: '{{ env "RAILS_ENV" | default "production" }}'
   tasks:
     migrate:
       envs:
         E: '{{ env "RAILS_ENV" }}' # resolves to "production" when the process var is unset
   ```
### Chore
- Removed diad code.
- Small refactoring for executor logging.

## [1.5.0] - 2026-07-06
### Added
 - Bastion (jump host) support: `ssh.bastion` routes every SSH connection through one jump host, like
   OpenSSH `ProxyJump` (single hop):

   ```yaml
   ssh:
     bastion:
       address: bastion.example.com
       user: jump
       identity_file: ~/.ssh/bastion_key
   ```
   The bastion connection is opened once, lazily on the first host dial, and shared - every host gets its
   own tunneled channel over it. The bastion authenticates like any host (its own `identity_file`, else the
   builtin agent, else the system ssh-agent) and its host key is verified with the same
   `strict_host_key`/`known_hosts_file`/`accept_new` settings. Agent forwarding never applies to the bastion
   itself. Local hosts bypass it, inventory-discovered hosts are tunneled like any other host.

 - A task run as its own CLI invocation (`whoosh <stage> <task>`) now fires the after `deploy:failed` hooks
   when it fails, so a pipeline run outside the deploy lifecycle (e.g. an ASG refresh) notifies like a
   failed deploy - the slack plugin's failure message, `{{.error}}` / `$DEPLOY_ERROR`, etc. all work.
   Opt a task out with the new `notify_failure: false` field (default `true`). Hook errors are logged
   best-effort, the command still exits with the task's own error.

 -  Docker image: `jq`, `yq`, `curl`, `wget`, `bash` packages.

## [1.4.0] - 2026-07-05
### Added
 - Builtin in-memory SSH agent, fed by the new `ssh.identities` map - so CI and multi-key setups need no
   `ssh-agent` on the operator machine.
   Each entry loads a key file, a directory of keys (`recursive` descends into subdirectories), or an inline PEM,
   with an optional `passphrase` for encrypted keys:

   ```yaml
   ssh:
     identities:
       app_hosts:
         path: ~/.ssh/id_app
       ci:
         content: '{{ env "CI_DEPLOY_KEY" }}'
         passphrase: '{{ envSecret "CI_KEY_PASS" }}'
   ```
   When `ssh.identity_file` or `ssh.identities` is set, whoosh authenticates with the builtin agent and the
   system ssh-agent (`SSH_AUTH_SOCK`) is no longer consulted.
   With `forward_agent: true` the builtin agent is what gets forwarded to the hosts (`forward_key` still takes precedence).
   `content` and `passphrase` are masking in the `config` dump, `{{.config}}`, and logs.

 - `identity_file_passphrase` decrypts an encrypted `identity_file`, at the `ssh:` level and per host.
   A host inherits the global pass phrase only together with the global `identity_file`.

 - Config `vars:` values are themselves Go templates, rendered once at config load against the static context
   (app/stage/paths, sprig, `env`/`envSecret`/`sensitive`) - so a var can pull from the environment:
   ```yaml
   env_files: [ ./dev.env ]
   vars:
     app_version: '{{ env "APP_VERSION" }}' # process env, else dev.env
   ```
   Limitations: a var cannot reference another var, `{{.config}}`, plugin imports, or run-time values
   (`release_path`/`host`/... render empty at load).

 - The `env`/`envSecret` template helpers now fall back to the `env_files` (dotenv) values when the process
   env var is unset (a set-but-empty process var still wins) - everywhere templates render: vars, plugin
   `params:`, `cmds`, scripts, `envs:`.

### Fixed
 - `whoosh <stage> config` now redacts registered secrets (e.g. `envSecret` values in vars or plugin params) in
   the dumped config, like every other output path. You can use `--log-level=debug` for show 'secrets' as plain text.
 - Configuration verification and validation process, now configuration validation works correctly for all phases.
 - Template check skips templates using run-time task state

## [1.3.0] - 2026-07-04
### Added
 - Template helpers: `toYaml`, `fromYaml`, `fromYamlArray`, and `required "msg" .val` (fail the render when a
   value is nil/empty) - the gaps sprig doesn't cover. The full sprig set (`toJson`, `join`, `default`, ...) was
   already available in every template and is now documented in
   [Templating & variables](https://whoosh.yousysadmin.com/configuration/templating/#helper-functions).

### Changed
 - Config `vars:` are no longer auto-exported as shell environment variables of task commands and scripts.
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
 - Plugins: bundled default-on `systemd` plugin - `systemd:start`/`stop`/`restart`/`enable`/`disable`/`daemon-reload`
   actions run `systemctl` on the task's hosts (system and `--user` units, optional `sudo -n`, `daemon_reload`,
   `--now`, `--no-block`), usable ad-hoc via `action:`/`with:` or auto-wired to a deploy phase via the plugin's
   `actions:` params (`phase`/`when`/`roles`).
 - Plugin SDK: `HostCommandRunner` - the command counterpart to `HostFileWriter`. The executor hands it to every
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
 - Deployfile.schema.json updated

### Fixed
 - Logs: small fixes for JSON log format
 - Docs: fix internal links and typos

### Added
 - Plugins: Slack plugin imported into Whoosh

## [1.0.0] - 2026-07-03

First public release.
Version changed from 8.3.1 to v1.0.0 - the new era

[Unreleased]: https://github.com/YouSysAdmin/whoosh/compare/v1.6.0...HEAD
[1.6.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.4.0
[1.5.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.4.0
[1.4.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.4.0
[1.3.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.3.0
[1.2.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.2.0
[1.1.1]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.1
[1.1.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.0
[1.0.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.0.0
