# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]
### Changed
 - The toolchain is Go 1.27 across every module, CI workflow, and the docs. The standard `encoding/json` is now
   backed by Go's new v2 engine with v1 semantics preserved - user-facing behavior (captured `output:` parsing,
   secret expansion, Slack payloads) and the generated `deployfile.schema.json` are byte-identical. The codebase
   is modernized to the current idioms (`maps.Copy`, `slices.Contains`, `strings.SplitSeq`/`Cut`, `errors.AsType`,
   `wg.Go`, generic `reflect.TypeFor`).
 - Deploys render templates much less often: the per-host render context (resolved global `envs:`, host roles) is
   built once and reused across a step's command, task envs, `dir:`, and echo, invalidated only when the deploy
   context actually changes. The echo also reuses the step's rendered command instead of rendering it again.
 - `whoosh <stage>` parses the config tree once for task and plugin command discovery - previously every
   invocation (and every shell completion) parsed it three times, including duplicate env-file reads.
 - `aws:secrets` fetches the secrets under a prefix with bounded concurrency (5 at a time) instead of one by one.
   Required IAM permissions are unchanged.
 - The plugin SDK's `whoosh.MergeParams` now copies nested maps instead of aliasing them, so an action mutating
   its merged params can no longer silently edit the plugin's shared action defaults.

### Fixed
 - Plugin failures now exit with the documented code 40: plugin configure/startup errors and action failures carry
   a typed `PluginError` (a more specific command/unreachable code still wins), and an unknown plugin name - in
   `validate` too - exits with the config code 10 instead of 1.
 - A task's `strict_host_key:` override is honored by plugin actions' host command and file-writer helpers -
   previously an action task rendering a file onto (or running commands on) ephemeral hosts dialed with the
   cluster default strictness and could fail known_hosts verification, unlike every other task path.
 - `aws:ec2:inventory` with no non-empty `tags` filter is a load error instead of silently inventorying every
   running instance in the region as deployable hosts. An instance dropped for lacking a public IP under
   `use_public_ip: true` is now logged instead of vanishing.
 - `aws:ec2:asg:rollback` / the launch-template patch no longer panic when `CreateLaunchTemplateVersion` returns a
   partial-success response without the version - it fails with a labeled error.
 - `connect_timeout` is a single budget over TCP connect plus SSH handshake (bastion included) - previously each
   phase got the full timeout, doubling the documented bound.
 - A dial aborted by a context deadline keeps its identity through the "connection timed out" wrap, so the shared
   bastion no longer caches a deadline-aborted dial as a permanent failure (`deploy:failed` hooks can reconnect).
 - SSH connections are pooled by the full target identity (address, port, user, identity file, host-key
   strictness, local flag) - two inventory entries sharing an address but differing in user or port no longer
   silently share whichever connection dialed first.
 - A dead or unreachable ssh-agent socket is no longer silent: the dial error is surfaced when no other auth
   method exists, instead of a misleading "start an ssh-agent" hint while `SSH_AUTH_SOCK` is set.
 - Two parallel first contacts with the same `accept_new` host must present the same key: a conflicting key is
   rejected with a host-key mismatch instead of both keys being appended to known_hosts and trusted forever.
 - Task discovery resolves `include:`s with the same shared state as the real load - when the Deployfile includes
   the stage file, `whoosh <stage> <task>` registration no longer reads task metadata (desc, hidden, stage gating)
   from a definition merged with the opposite precedence.
 - The load-time template check no longer skips static `.config.tasks.*` references - only templates reading
   run-time task state (`{{ .tasks.* }}`) are exempt from the offline render check.
 - A typo'd feature params key under an `actions:` entry (aws ec2/ssm/secrets) is a load error instead of silently
   disabling the feature - validated against the union of the entry's consumers, so legitimate shared keys still
   pass.
 - An unknown `--log-level` (or `log: level:`) value is an error instead of silently logging at INFO.
 - The line-buffering writers (output prefixing, masking) honor the `io.Writer` contract on a failed write - the
   consumed bytes are reported and the failed line is dropped, so a retrying caller no longer re-emits it.
 - A local task's daemonizing grandchild that escapes the process group can no longer hang the run after a cancel -
   the local transport abandons the output pipes after a short delay (`WaitDelay`).

### Security
 - AWS credentials are registered with the secret masker in every resolution path - static params,
   `credentials_file`, `credentials_url` (token included), and IMDS-over-SSH - so `whoosh <stage> config` and logs
   can no longer print a `secret_access_key` in cleartext.
 - The slog narrative (phase logs, warnings, error records) is masked like every raw output path - a registered
   secret embedded in an error message no longer reaches the console or log files. Debug level still shows raw
   values, as before.

## [1.7.0] - 2026-07-28
### Added
 - Hook validation at config load: every `hooks:` key must be a deploy phase, a custom phase, `deploy:failed` /
   `deploy:rollback`, or an existing task, and every hooked task must exist. A typo'd key (or a plugin's misspelled
   `phase:` param) now fails the command instead of silently never firing. `deploy:failed` only fires `after` hooks,
   so a `before:` entry for it is rejected too. The check runs after plugin startups, so hooks may reference
   plugin-contributed tasks and phases.
 - Plugin SDK:
   - `whoosh.DecodeParamsStrict` - `DecodeParams` with unknown keys rejected, so a misspelled param errors at load
     instead of silently applying the default.
   - `whoosh.MergeParams` - layer a task's `with:` over a plugin's action defaults (nested maps merge recursively).
   - `whoosh.Or` / `whoosh.OrZero` - defaults for optional pointer and scalar params.

### Changed
 - `aws:ec2:asg:rollback` now **cancels** an instance refresh already in flight (typically the bad deploy's own
   rollout) and starts its own refresh in its place. Previously the rollback skipped the refresh and reported
   success while the fleet kept rolling onto the version being rolled back. `aws:ec2:asg:refresh` still skips an
   in-flight refresh as before.
 - The aws, slack, rbenv, and systemd plugins now reject misspelled params (strict decoding): a typo like
   `use_public_iq:` is a load error instead of a silently applied default. rbenv also rejects unknown `actions:`
   entries and an invalid `when:` value (which previously fell back to "before").

### Fixed
 - SSH: the handshake is now bounded by the connect timeout and the command's context. A host that accepts TCP but
   never answers SSH (a wedged sshd, a tarpit firewall) used to hang the whole run with no `Ctrl-C` escape - only
   the TCP dial was bounded, and keepalive starts after the handshake. Works through a bastion too.
 - SSH: a dial aborted by a cancelled context (an operator `Ctrl-C`, or a fail-fast sibling failure) is no longer
   cached as a permanent failure - neither per host nor for the shared bastion connection. `deploy:failed` hooks,
   which run on a fresh context after a cancel, can reach those hosts again.
 - `on_unreachable: skip` is now honored by hook tasks, custom-phase tasks, and the mid-deploy commit-hash read -
   previously only the built-in phase steps dropped an unreachable non-required host, and a hook hitting a dead
   host aborted the whole deploy, contrary to the documented behavior.
 - An `output:` task whose role/host filters match no hosts now stores its format's zero value (like dry-run does),
   so a later `{{ .tasks.<name> }}` reference renders instead of failing strict rendering far from the cause.
 - Secret redaction gaps:
   - Plugin-contributed CLI commands (e.g. `deploy:hosts`) now write through the masking writer like every other
     output path.
   - `aws:ssm:to-dotenv` and `aws:secrets:to-dotenv` now register every fetched value for masking, like the startup
     import path - a later command printing the env can no longer leak them.
 - `whoosh <stage> run` now gets the same environment layering as task commands: `env_files` as the base layer and
   plugin imports as `$<NS>_<KEY>`, so `echo $FROM_DOTENV` / `echo $SSM_TOKEN` behave like the identical `cmds:`
   line in a task.
 - `{{ .keep_releases }}` now resolves to the real value in load-time templates (`vars:`, plugin params, global
   `envs:`) - it used to render `0`.
 - `--deployfile` is now honored when registering task and plugin commands, so named tasks are invocable when the
   Deployfile lives outside the current directory.
 - A fragment included by both the shared `Deployfile` and the stage file is merged once - previously it merged
   twice, and a duplicated host entry ran every command twice in parallel and raced building the same release dir.
 - `aws:ec2:ami:cleanup` paginates `DescribeImages`, so cleanup keeps pruning in accounts with more than one page
   of images.
 - `aws` `credentials_from_host`: the IMDS fetch now fails with a labeled error on an HTTP error (e.g. the instance
   has no IAM instance profile) instead of parsing the error body as data.
 - A failed deploy-lock release now logs a warning pointing at `deploy:unlock` instead of being silently ignored.
 - Smaller leaks and error-reporting fixes: the ssh-agent socket is closed after each handshake, the `--log-output`
   file handle is closed on logging reconfigure, a never-dialed bastion refuses dials after `Close`, `deploy:hosts`
   surfaces table-render errors, and the in-process SSH test server kills orphaned commands on disconnect and no
   longer reports success for non-exit failures.

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

[Unreleased]: https://github.com/YouSysAdmin/whoosh/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.7.0
[1.6.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.6.0
[1.5.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.5.0
[1.4.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.4.0
[1.3.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.3.0
[1.2.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.2.0
[1.1.1]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.1
[1.1.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.1.0
[1.0.0]: https://github.com/YouSysAdmin/whoosh/releases/tag/v1.0.0
