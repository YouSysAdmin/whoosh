# 04 - AWS dynamic inventory (vars-driven, multi-stage)

Discover deploy targets from EC2 tags instead of hardcoding IPs, bake/roll AMIs, and refresh an Auto Scaling Group -
across stages **without copying any plugin or task blocks**, and with AWS switched **off** for a stage that doesn't
need it.
One `aws` plugin (compiled into the binary) provides every feature, and the shared `Deployfile.yml` configures it
once.

## Files

```
Deployfile.yml          # the LOGIC: the aws plugin (via vars), tasks, hooks - written once
deploy/production.yml   # just vars: region, bastion, asg_name, ...
deploy/uat.yml          # just vars (+ a branch override) - a different account/region
deploy/staging.yml      # no aws: the plugin's `except: [staging]` switches it off here
```

The headline idea: **logic in the shared file, values in the stage files.**
The plugin's `params:` and the actions' `with:` are Go-templated against the stage's `vars` (plus static config and
sprig `{{ env }}`), so a stage is just a block of vars.
Compare `deploy/production.yml` and `deploy/uat.yml` - that small diff is the whole difference between the two
environments.

## Run (needs AWS access for the AWS stages)

```sh
whoosh uat hosts               # resolve inventory and print the host table - no deploy
whoosh production deploy       # deploy + (via hooks) bake AMI, roll ASG, prune
whoosh production bake-ami     # just build an AMI and patch the launch template
whoosh production asg-refresh  # just roll the ASG and wait
whoosh production asg-rollback # roll the launch template back a version, then refresh
whoosh staging deploy          # aws is OFF here - bake/roll/prune are skipped, restart runs
```

## How the vars-driven layout works

- **One plugin, configured once.** The shared file declares a single `aws` plugin.
  Its global `params` hold the AWS connection (region + bastion credentials) shared by every feature, and `actions:`
  configures `aws:ec2:inventory` (which needs tag filters). The asg/ami actions are available without being listed.
- **Values, per stage.**
  `region: "{{ .aws_region }}"` and `credentials_from_host: { host: "{{ .bastion }}", ... }` pull from `vars` that
  each `deploy/<stage>.yml` sets. Same for the tasks' `with:` (`asg`, `name_prefix`, ...).
- **AWS off for staging.**
  The plugin lists `except: [staging]`, so in `staging` it isn't loaded - inventory doesn't run (and no bastion is
  contacted), and the `bake-ami`/`asg-refresh`/`asg-rollback`/`prune-amis` action tasks are **skipped** (logged), not
  failed. `restart` still runs. `staging.yml` therefore needs no AWS vars - it just lists hosts statically.
- **Quote templated values.**
  YAML reads a leading `{` as a flow-mapping, so write `host: "{{ .bastion }}"`, not `host: {{ .bastion }}`.
  Quote bool/number-looking tag values too (`Deploy: "true"`).
- **When rendering happens.** The plugin's `params:` render at startup (so `whoosh uat config` shows them resolved).
  Action `with:` renders when the action runs (so `config` still shows `{{ .asg_name }}` there - that's expected).

## The immutable-infrastructure flow

The AMI/ASG actions chain into bake -> roll -> prune, wired into `after: deploy:published`:

1. **`aws:ec2:ami:create`** - pick a source instance (precedence: explicit `instance_id`, first running instance
   matching `source_tags`, or first `InService` in the ASG), image it (tags copied over, `Name`/`aws:*` dropped), wait
   until available, then - when `launch_template:` is given - clone the template's `$Default` with the new AMI and
   make it the default.
2. **`aws:ec2:asg:refresh`** - roll the ASG so instances relaunch from that new default, then poll until the refresh
   completes, failing if it ends `Failed`/`Cancelled`/rolled-back.
3. **`aws:ec2:ami:cleanup`** - deregister old AMIs (matched by `name_prefix` and/or `tags`) and delete their
   snapshots, keeping the newest `keep_last`.

To undo a bad rollout, **`aws:ec2:asg:rollback`** copies the launch template's previous version forward and refreshes
onto it (the `asg-rollback` task).

`hosts` is the safe first step: it runs the inventory plugin and prints every matched instance (including `deploy:
false` ones) without contacting them.

## Concepts shown

- **Vars-driven stages** - one `aws` plugin plus tasks/hooks defined once, while stages supply only vars.
  The point of this example.
- **Per-stage activation** - `except: [staging]` switches the plugin off for a stage, its action tasks are then
  skipped (logged), not failed.
- **Templated plugin params** - `credentials_from_host`, `region`, and `tags` driven by vars, with one global
  credential block shared by every feature.
- **Startup inventory** - `aws:ec2:inventory` (under `actions:`) appends tag-matched instances to `servers` before any
  action runs.
- **`deploy_tag` / `required_tag`** - ship only to instances carrying `deploy_tag` (the rest stay listed as `deploy:
  false`), and never skip `required_tag` instances.
- **Roles from tags** - `role_tag` reads roles from an instance's tag value, falling back to `roles`.
- **Action tasks** - `action:` + `with:` invoke a plugin action operator-side.
- **Credential sources** - here, reading temporary IAM creds from a reachable instance's IMDSv2 over SSH.
  Other sources (static keys, profile, local/remote YAML, default chain) are in
  [docs/plugins.md](../../docs-old/plugins.md#aws-credentials).

## Note

Because the inventory plugin contacts AWS at startup, `config`/`hosts`/`deploy` all require working credentials and
reachable resources. Edit the vars in the stage files to match your account before running.
