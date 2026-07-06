---
title: "Hooks & phases"
description: "Attach tasks to the deploy lifecycle with before/after hooks, the special deploy:failed and deploy:rollback hooks, and phase replacement."
weight: 80
---

A deployment runs a fixed sequence of **phases**.
You attach tasks to a phase with `hooks.before` / `hooks.after`, keyed by phase name:

```yaml
hooks:
  after:
    deploy:updated: [ bundle, assets, migrate ]   # release built, not yet live
    deploy:published: [ restart, healthcheck ]      # release is now live
```

The phase order:

```ascii
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

- The **marker phases** (`deploy:init`, `deploy:started`, `deploy:updated`, `deploy:published`, `deploy:finished`) run
  no built-in command - they are stable hook anchors.
  **Prefer these for your own hooks**: `deploy:init` to provision the host (see below), `deploy:updated` for "release
  built but not yet live" (bundle/assets/migrate), and `deploy:published` for "release is live" (restart/healthcheck) -
  rather than binding to internal step names like `symlink`/`publishing`.
  For a marker phase, `before` and `after` are the same point.
- **`deploy:init`** fires right after the directory tree is ensured but before the release is built - the place to
  install OS packages, ensure a language runtime, or otherwise prepare the host.
  Because the release directory doesn't exist yet, give such a task an explicit `dir:` pointing at an existing path
  (`dir:` is Go-templated, so `dir: "{{.deploy_to}}"` works):

  ```yaml
  tasks:
    provision:
      desc: Install OS packages the app needs
      roles: [app, web]
      dir: "{{.deploy_to}}"
      scripts:
        - path: provision.sh
          interpreter: /usr/bin/env bash
  hooks:
    before:
      deploy:init: [provision]
  ```
- **`deploy:failed`** - a special key whose `after` tasks run when a deploy errors (the deploy still fails afterward).
  The failure message is exposed as `{{.error}}` / `$DEPLOY_ERROR`. Good for failure notifications.
  A task run as its own CLI invocation (`whoosh <stage> <task>`) fires these hooks on failure too, so a post-deploy
  pipeline (e.g. an ASG refresh) notifies like a failed deploy - a task opts out with `notify_failure: false`.
- **`deploy:rollback`** - `before`/`after` tasks wrap the symlink swap of `whoosh <stage> deploy:rollback`, and
  `after` tasks run with `current` already repointed at the restored release.
  See [`examples/07-rails-assets`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/07-rails-assets).
  A task can also **replace** the built-in swap entirely with `replace: deploy:rollback` (e.g. an
  `aws:ec2:asg:rollback` action), so `whoosh <stage> deploy:rollback` does the app-specific thing while staying one
  command. The hooks still run around it, and `--cleanup` doesn't apply to a replaced rollback.

## Task-to-task hooks

A hook key can also be a **task name** instead of a phase. The named tasks then run before / after *that task* every
time it runs - whether invoked directly (`whoosh <stage> <task>`), pulled in as a `deps:` dependency, or fired from
another hook - without binding either task to a phase:

```yaml
hooks:
  after:
    restart_sidekiq: [ push_to_newrelic ]   # whenever restart_sidekiq runs, notify afterward
  before:
    restart_sidekiq: [ drain_queues ]        # ...and drain first
```

Ordering around the wrapped task: its `before` hooks run first, then its own `deps:`, then the task body, then its
`after` hooks. A hook that loops back to the task it wraps is rejected as a dependency cycle.

This is the difference from `deps:`: a dependency is declared on the *consuming* task and only pulls work in *before*
it, whereas an `after:` task hook lets an unrelated task attach work *after* another without editing it.

A hook task can read which phase it is running for via `{{.phase}}` / `$DEPLOY_PHASE`, so one templated script can
handle start / success / failure - see
[`examples/06-slack-notify`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/06-slack-notify).

See [Usage -> Deploy lifecycle](/usage/#deploy-lifecycle) for what each phase *does*.
