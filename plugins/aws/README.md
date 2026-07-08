# Whoosh AWS Plugin

An AWS plugin ships as a **separate module** (`github.com/yousysadmin/whoosh/plugins/aws`) - it's not in the default
binary (it would pull the ~57 MB AWS SDK).
Add it with a custom build (`whoosh build --with github.com/yousysadmin/whoosh/plugins/aws`), then list it under
`plugins:`. Listing it activates the plugin and all its features.
They share one connection (region + credentials) set in the global `params`, and per-feature config goes under
`actions:`. Each action is named `<feature>:<action>` (e.g. `aws:ec2:asg:refresh`).
Use `only:`/`except:` to limit the plugin to specific stages - when it's inactive for a stage, its action tasks are
**skipped** (logged), not failed.

```yaml
plugins:
  - name: aws
    except: [staging]              # active everywhere except staging (which has no AWS)
    params:                        # global: region + ONE credential source, shared by all features
      region: eu-west-1
      credentials_from_host: { host: "{{ .bastion }}", user: deploy }
    actions:
      # ec2:inventory is a startup feature, listed because it needs tag filters.
      - name: aws:ec2:inventory
        params:
          # each tag value is a string or list (matches any value), keys AND-ed.
          tags: { Environment: [uat, staging], App: myapp }
          role_tag: deployer:roles   # optional: tag value (comma-separated) -> roles
          roles: [app]               # fallback roles when role_tag is absent
          use_public_ip: false       # default: private IP
          deploy_tag: { Name: Deploy, Value: "true" }      # only matches deploy, rest listed, not deployed
          required_tag: { Name: Critical, Value: "true" }  # tag-matching instances are required: true
          resolve_config_hosts: true # dedup discovered IPs against the static hosts' resolved FQDNs
      # The asg/ami actions are available whether or not they're listed here.
      - name: aws:ec2:asg
      - name: aws:ec2:ami

hosts:                          # static hosts still allowed, merged with discovered
  - address: db1.example.com
    roles: [db]

# An action task (runs on your machine, not over SSH) calls a registered action:
tasks:
  refresh_asg:
    desc: Roll the ASG and wait for the refresh to finish
    action: aws:ec2:asg:refresh
    with:
      name: my-asg
      # all preferences optional, sensible defaults:
      min_healthy_percentage: 100   # default 100
      max_healthy_percentage: 200   # default 200
      instance_warmup: 300          # default 300 (seconds)
      skip_matching: true           # default true (skip instances already on the new LT)
      auto_rollback: false          # default false

  # Manual rollback: copy the launch template's previous version forward to a new
  # latest, then refresh onto it (the counterpart to a forward aws:ec2:ami:create).
  rollback_asg:
    desc: Roll the launch template back a version and refresh
    action: aws:ec2:asg:rollback
    with:
      name: my-asg                  # the ASG to roll back + refresh
      # launch_template defaults to the ASG's own, set_default defaults true,
      # refresh preferences (min/max_healthy, ...) are the same as aws:ec2:asg:refresh.

  # Bake an AMI from a live instance and point the ASG's launch template at it.
  bake_ami:
    desc: Build an AMI and update the launch template
    action: aws:ec2:ami:create
    with:
      name_prefix: myapp                 # AMI named "<prefix>-<timestamp>"
      # source instance - set one (precedence: instance_id > source_tags > asg):
      asg: my-asg                        # first InService instance in this ASG
      # instance_id: i-0abc123           # a specific instance
      # source_tags: { Role: web }       # first running instance matching these tags
      # launch_template is OPTIONAL - omit it to only bake an AMI (no launch
      # template is touched), include it to point one at the new AMI:
      launch_template: { asg: my-asg }   # this ASG's launch template, or:
      # launch_template: { id: lt-0abc123 }

  prune_amis:
    desc: Remove old AMIs and their snapshots
    action: aws:ec2:ami:cleanup
    with:
      name_prefix: myapp                 # and/or tags: (set at least one)
      tags: { Application: myapp, Environment: production }
      keep_last: 3
```

`aws:ec2:asg:refresh` starts an instance refresh (sending the full preference set above) and then **polls until it
finishes**, logging `status`/`percent` each interval - so the task blocks until the rollout completes and fails if the
refresh ends `Failed`/`Cancelled`/rolled-back.
A refresh already in progress is logged and skipped rather than treated as an error.

A typical immutable-infrastructure deploy wires these as a chain - bake -> roll -> prune:

```yaml
hooks:
  after:
    deploy:publishing: [bake_ami, refresh_asg, prune_amis]
```

`aws:ec2:ami:create` picks a source instance (precedence: an explicit `instance_id`, the first running instance
matching `source_tags`, or the first `InService` instance in `asg`), copies its tags onto the image (dropping `Name`
and `aws:*`, setting `Name` to the AMI name), waits until the image is available, and - **only when `launch_template:`
is given** - clones the template's `$Default` version with the new AMI and makes it the default.
Omit `launch_template:` to just bake an AMI and leave every launch template untouched (e.g. to patch it separately, or
to keep a rolling image library).
`no_reboot` defaults to true, and `wait` defaults to true (and is forced when patching a launch template).
`aws:ec2:ami:cleanup` deregisters self-owned AMIs matching its filters - `name_prefix` (Name starts with `<prefix>-`)
and/or `tags` (each value a scalar or list, AND-ed across keys) - keeping the newest `keep_last` (default 3), and
deletes their backing snapshots. At least one filter is required so a misconfiguration can't prune every image.

- To see the discovered hosts (and their deploy flag) run `whoosh <stage> deploy:hosts`, which prints the resolved
  inventory as a table including `deploy: false` hosts. It works for any inventory source.
  (The command is provided by the default-on `print-hosts-table` plugin - it replaced the former built-in `hosts`
  command, demonstrating that plugins can add CLI commands.)
  With `deploy_tag` set, only instances carrying that tag are deployed to, while the rest are still listed.
- A statically declared host always wins over a discovered duplicate (same address). With
  `resolve_config_hosts: true` the dedup also resolves static FQDN addresses to IPs (on the operator's machine),
  so a host declared by name and discovered by EC2 by IP is not listed twice. A failed lookup only warns.
- **Per-stage activation**: `only: [stages]` / `except: [stages]` on the plugin limit which stages it loads in.
  When inactive for a stage, its startup never runs (no inventory, no AWS contact) and its action tasks are
  **skipped** (logged), not failed - so e.g. a `staging` stage with no AWS just no-ops the AWS tasks.
  A genuinely unknown action still errors.
- An action task can't be combined with `cmds`/`scripts`. It runs once, operator-side.
  `--dry-run` prints the call without contacting AWS.
  (The `aws:ec2:inventory` startup runs on every command, so it does reach AWS.)
- `with:` values are **Go-templated** (string values at any nesting depth), rendered against `vars` and the deploy
  context - so `name: "{{ .asg_name }}"` or `name: "{{ .stage }}-asg"` work, while numbers and booleans pass through
  unchanged.
- Plugin **`params:` are templated too** (against `vars` + static config + sprig `{{ env }}`, since plugins load
  before any release exists).
  So you can keep the plugin logic in the shared `Deployfile.yaml` and vary only the values per stage - e.g.
  `credentials_from_host: { host: "{{ .bastion }}", user: "{{ .deploy_user }}" }` with `bastion`/`deploy_user` set in
  each `deploy/<stage>.yaml`'s `vars:`.
- **Quote templated values**: YAML reads a leading `{` as a flow mapping, so `host: {{ .bastion }}` is a parse error -
  write `host: "{{ .bastion }}"`. Quote bool/number-looking tag values too (`Deploy: "true"`).

#### AWS credentials

Credentials go in the `aws` plugin's **global `params:`**, shared by every feature (one connection is built).
Set `region` (or let the standard AWS chain resolve it) and pick **one** credential source.
With none set, the SDK default chain is used (env vars, shared config/`profile`, and the EC2 instance IAM role):

```yaml
params:
  region: eu-west-1
  profile: myprofile            # shared-config profile (AWS_PROFILE)

  # or static keys:
  access_key_id: AKIA...
  secret_access_key: ...
  session_token: ...            # optional

  # or a local YAML file of aws_* keys:
  credentials_file: /run/secrets/aws.yaml

  # or fetch that YAML over HTTP (e.g. a private GitHub raw URL):
  credentials_url: https://raw.githubusercontent.com/org/repo/main/aws.yaml
  credentials_token: ghp_...    # sent as "Authorization: token <token>"

  # or read temporary creds from a remote host's EC2 instance metadata over SSH:
  credentials_from_host:
    host: bastion.example.com
    user: deploy                # optional (default: $USER)
    identity_file: ~/.ssh/id    # optional, ssh-agent is also used
    # port / strict_host_key / known_hosts_file also accepted
```

The file/URL YAML uses these keys:

```yaml
aws_access_key_id: AKIA...
aws_secret_access_key: ...
aws_session_token: ...          # optional
aws_default_region: eu-west-1   # optional (also accepts aws_region)
```

Source precedence: static keys, `credentials_file`, `credentials_url`, then `credentials_from_host`.
A region from the credentials file/URL/host is used when `region:` is not set.
With no source set, the SDK default chain applies (env vars, shared config/`profile`, and the **local** EC2 instance
IAM role). By contrast, `credentials_from_host` is for reading a **remote** instance's role over SSH.

Adding a plugin means implementing `plugin.Plugin` (`Configure(spec, reg)`, where `spec` is the whole `PluginSpec` -
global params + `actions:`) and registering it by name (see `plugins/aws`).
In `Configure` it validates the spec and calls `reg.AddStartup(...)` and/or `reg.AddAction(...)`.
The core only runs startup hooks, looks up actions by name, and applies the `only`/`except` stage filter - it never
references a plugin directly.
