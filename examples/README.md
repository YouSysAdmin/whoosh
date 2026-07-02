# Examples

Worked `whoosh` configurations, smallest to richest.
Each directory is a self-contained project: `cd` into it and run `whoosh <stage> <action>`.

| Example | Shows | Needs |
|---|---|---|
| [`01-minimal`](01-minimal/) | The smallest useful Deployfile: one app, one stage, the bare release lifecycle | SSH hosts (or just `--dry-run`) |
| [`02-rails-multistage`](02-rails-multistage/) | A realistic multi-stage app: roles, `vars` vs `env`, rbenv `PATH`, hooks, file scripts, conditional `db:migrate` | SSH hosts |
| [`03-local-mode`](03-local-mode/) | Running the **whole lifecycle locally** with no SSH (`local: true`) | nothing - runs on your machine |
| [`04-aws-inventory`](04-aws-inventory/) | The `aws` plugin: EC2 inventory + AMI/ASG actions, **vars-driven across stages**, and a `staging` stage where aws is switched off (`except:`) so its tasks are skipped | AWS access |
| [`05-tasks-and-scripts`](05-tasks-and-scripts/) | A cookbook of task & templating features (deps, `once`, `dir`, `env`, inline/file scripts, `{{.config}}`, the `deploy:hosts` host table) | nothing - runs locally |
| [`06-slack-notify`](06-slack-notify/) | Send a Slack message from a deploy hook - one templated script branches on `{{.phase}}` for start / success / failure | a Slack webhook (runs locally) |
| [`07-rails-assets`](07-rails-assets/) | Two asset strategies side by side - per-release (rollback is automatic) vs shared `public/assets` (manifest backup/restore). Pick one per app | SSH hosts (or `--dry-run`) |

## Trying an example without infrastructure

Two things work on any example without touching a host:

```sh
cd examples/01-minimal
whoosh production config          # print the resolved, merged config
whoosh production deploy --dry-run   # print the exact plan, no SSH
```

`03-local-mode` and `05-tasks-and-scripts` run for real on your machine - no hosts required.

## How a deploy works

Every `deploy` runs the same ordered phases on each host, with your `hooks.before` / `hooks.after` tasks wrapped
around each one:

```
    deploy:starting ---> deploy:check ---> deploy:init ---> deploy:started
    ----------------------------------+------------------------------------
                                      |
                                      v

deploy:updating ---> deploy:symlink ---> deploy:updated ---> deploy:publishing
--------------------------------------+---------------------------------------
                                      |
                                      v

        deploy:published ---> deploy:finishing ---> deploy:finished
        -----------------------------------------------------------
```

The marker phases (`deploy:init` / `started` / `updated` / `published` / `finished`) run no built-in command - they're
stable hook anchors.
Prefer `deploy:init` (provision the host - install software), `deploy:updated` (release built, not yet live), and
`deploy:published` (release live) for your own hooks.

It builds a timestamped release under `<deploy_to>/releases/`, symlinks the shared files/dirs into it, atomically
swaps `<deploy_to>/current`, and prunes old releases.
`whoosh <stage> deploy:rollback` repoints `current` at the previous release, firing `before`/`after` `deploy:rollback`
hooks around the swap.

For the full list of template values (`{{.release_path}}`) and their `$ENV` equivalents (`$RELEASE_PATH`), and all CLI
flags, see the project [`README.md`](../README.md).
