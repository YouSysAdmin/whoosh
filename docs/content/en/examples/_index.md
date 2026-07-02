---
title: "Examples"
description: "Worked, self-contained Deployfile configurations - smallest to richest. cd into one and run whoosh <stage> <action>."
weight: 60
icon: info

---

Worked `whoosh` configurations, smallest to richest.
Each directory in the repo's [`examples/`](https://github.com/YouSysAdmin/whoosh/tree/main/examples) is a
self-contained project: `cd` into it and run `whoosh <stage> <action>`.

| Example                                                                                                 | Shows                                                                                                                                                                  | Needs                           |
|---------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------|
| [`01-minimal`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/01-minimal)                     | The smallest useful Deployfile: one app, one stage, the bare release lifecycle                                                                                         | SSH hosts (or just `--dry-run`) |
| [`02-rails-multistage`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/02-rails-multistage)   | A realistic multi-stage app: roles, `vars` vs `envs`, rbenv `PATH`, hooks, file scripts, conditional `db:migrate`                                                      | SSH hosts                       |
| [`03-local-mode`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/03-local-mode)               | Running the **whole lifecycle locally** with no SSH (`local: true`)                                                                                                    | nothing - runs on your machine  |
| [`04-aws-inventory`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/04-aws-inventory)         | The `aws` plugin: EC2 inventory + AMI/ASG actions, **vars-driven across stages**, and a `staging` stage where aws is switched off (`except:`) so its tasks are skipped | AWS access                      |
| [`05-tasks-and-scripts`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/05-tasks-and-scripts) | A cookbook of task & templating features (deps, `once`, `dir`, `envs`, inline/file scripts, `{{.config}}`, the `deploy:hosts` host table)                              | nothing - runs locally          |
| [`06-slack-notify`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/06-slack-notify)           | Send a Slack message from a deploy hook - one templated script branches on `{{.phase}}` for start / success / failure                                                  | a Slack webhook (runs locally)  |
| [`07-rails-assets`](https://github.com/YouSysAdmin/whoosh/tree/main/examples/07-rails-assets)           | Two asset strategies side by side - per-release (rollback is automatic) vs shared `public/assets` (manifest backup/restore). Pick one per app                          | SSH hosts (or `--dry-run`)      |

## Trying an example without infrastructure

Two things work against any example without touching a host:

```sh
cd examples/01-minimal
whoosh production config              # print the resolved, merged config
whoosh production deploy --dry-run    # print the exact plan, no SSH
```

`03-local-mode` and `05-tasks-and-scripts` run for real on your machine - no hosts required.

## Where to go next

- The phase-by-phase walk-through is in [Usage -> Deploy lifecycle](/usage/#deploy-lifecycle).
- Every template value (`{{.release_path}}`) and its `$ENV` equivalent (`$RELEASE_PATH`) is listed in [Configuration
  -> Templating & variables](/configuration/templating/).
