# 02 - Rails, multi-stage

Two stages, roles, lifecycle hooks, rbenv on `PATH`, and a health-check script.
This is the "full" example - most real Deployfiles look like this.

## Files

```
Deployfile.yml                          # shared: app, linked files/dirs, vars, env, tasks, hooks
deploy/staging.yml                      # one all-in-one host, `develop` branch
deploy/production.yml                   # per-role hosts (web / worker / db)
deploy/scripts/healthcheck.sh           # local healthcheck script
deploy/scripts/conditional_migrate.sh   # migrate only when db/ changed
```

## Run

```sh
whoosh staging deploy                 # deploy develop to the staging box
whoosh production deploy              # deploy main to the production fleet
whoosh production deploy --roles web  # restrict to web hosts only
whoosh production migrate             # run just the migrate task
whoosh production deploy --dry-run    # preview the full plan
```

## Concepts shown

- **Roles** - a task with `roles: [db]` runs only on db hosts, and `--roles web` narrows any action further.
  `migrate` uses `once: true` so it runs on a single db host, not all of them.
- **Conditional migrate** - `migrate` runs `deploy/scripts/conditional_migrate.sh`, which diffs the new release's
  `db/` against the live release's and skips `rails db:migrate` when nothing changed.
  It runs from a hook *before* the swap, so `$CURRENT_PATH` is still the old release - exactly the comparison you
  want.
- **`vars` vs `env`** - `vars.RAILS_ENV` is used as both `{{.RAILS_ENV}}` and `$RAILS_ENV`.
  `env.PATH` is shell-expanded (`$HOME`/`$PATH` resolve at run time) so rbenv shims are found in the non-login SSH
  shell.
- **Release-dir default** - task commands run inside the new release, so `bundle`/`rails` find the Gemfile (no `dir:`
  needed).
- **Hooks** - `bundle`/`assets`/`migrate` run after `deploy:updated` (the release is built and shared config linked
  in, but not yet live), `restart-*`/`healthcheck` run after `deploy:published` (the release is live).
  These `-ed` phases are stable hook anchors - prefer them over internal step names like `deploy:symlink`.
- **Stage overrides** - `staging.yml` overrides `branch`, `deploy_to`, and `RAILS_ENV`, and replaces the host list.
- **Per-server SSH** - `db1` connects as `dbadmin` while the rest use the `ssh.user` default.
- **Unreachable-host policy** - `on_unreachable: skip` finishes the deploy on the reachable hosts if one drops out
  (exiting non-zero), while `db1` is `required: true` so losing the database host still aborts.

> `forward_agent: true` requires the target's `sshd` to allow agent forwarding (`AllowAgentForwarding yes`) for the
> remote `git` to use your key.
