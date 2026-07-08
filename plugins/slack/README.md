# Whoosh Slack Plugin

Posts deploy notifications to a Slack [incoming webhook][webhook], plus a `slack:send` action any task can invoke for
a custom message.

[webhook]: https://api.slack.com/messaging/webhooks

## Install

The plugin is its own module (`github.com/yousysadmin/whoosh/plugins/slack`). It ships in the default `whoosh`
binary but not in `whoosh-core` - build a core-based binary that includes it with the `whoosh build` subcommand:

```sh
whoosh build --with github.com/yousysadmin/whoosh/plugins/slack -o ./whoosh
# from a local whoosh checkout (until subdirectory tags are published):
whoosh build \
  --replace github.com/yousysadmin/whoosh=. \
  --with github.com/yousysadmin/whoosh/plugins/slack \
  --replace github.com/yousysadmin/whoosh/plugins/slack=./plugins/slack \
  -o ./whoosh
```

`whoosh plugins` lists what a binary contains. Then activate it by listing it under `plugins:`:

```yaml
plugins:
  - name: slack
    params:
      webhook_url: '{{ env "SLACK_WEBHOOK_URL" }}'   # required
      channel: "#deploys"                            # optional; legacy webhooks only
      notify_rollback: true                          # off by default
```

With just that, every `whoosh <stage> deploy` posts:

- **started** (blue, before `deploy:starting`) - app, stage, release id
- **succeeded** (green, after `deploy:finished`) - app, stage, commit, release id, duration
- **failed** (red, after `deploy:failed`) - app, stage, the failure message, duration
- **rolled back** (yellow, after `deploy:rollback`) - opt-in via `notify_rollback: true`

Notifications fire only during the deploy lifecycle (never for `config`, `run`, or standalone tasks) and are
**best-effort**: a Slack outage is logged as a warning and never fails (or "un-succeeds") a deploy.

## Params

| Param | Default | Description |
|---|---|---|
| `webhook_url` | - (**required**) | Incoming-webhook URL, the default for every notification. Registered as a masked secret; typically `'{{ env "SLACK_WEBHOOK_URL" }}'`. |
| `webhook_start` / `webhook_success` / `webhook_fail` / `webhook_rollback` | `webhook_url` | Per-event webhook overrides - e.g. route failures to an alerts channel's webhook while everything else uses the default. |
| `channel` | webhook default | Override the target channel. Honored by **legacy** webhooks only - Slack-app webhooks ignore it. |
| `username` | webhook default | Override the sender name (legacy webhooks only). |
| `icon_emoji` | webhook default | Override the sender icon, e.g. `":package:"` (legacy webhooks only). |
| `notify_start` | `true` | Post when a deploy starts. |
| `notify_success` | `true` | Post when a deploy finishes successfully. |
| `notify_fail` | `true` | Post when a deploy fails (this is also what enables the `deploy:failed` hook). |
| `notify_rollback` | `false` | Post after `whoosh <stage> deploy:rollback`. |
| `message_start` / `message_success` / `message_fail` / `message_rollback` | built-in | Per-event message template overrides (see below). |
| `color_start` / `color_success` / `color_fail` / `color_rollback` | built-in | Per-event attachment-bar color overrides: `good`, `warning`, `danger`, or `#rrggbb`. |
| `rich_fields` | `false` | Structured success/fail message: a fields table with User, Stage, Branch, Revision, Duration, and Release (the release path, the duration moves from the text into the table). |
| `changelog` | disabled | Post the commits between the previously deployed revision and the new one on the success notification (see below). |
| `deployer_github_lookup` | `false` | Resolve the deployer to their GitHub display name when it looks like a login (e.g. `GITHUB_ACTOR` in CI). One unauthenticated API call per process, any failure falls back to the login. Used in the `rich_fields` User field. |
| `timeout` | `"10s"` | Bound on each webhook POST (Go duration). |

### Message templates

Messages are Go templates rendered with the full deploy context - `{{.app_name}}`, `{{.stage}}`, `{{.commit_hash}}`,
`{{.release_timestamp}}`, `{{.error}}` (in the fail message), your `vars`, sprig helpers, Slack mrkdwn. Defaults:

```
started:   :rocket: *{{.app_name}}* deploy to *{{.stage}}* started (release {{.release_timestamp}})
succeeded: :white_check_mark: *{{.app_name}}* deployed to *{{.stage}}* ({{ trunc 7 .commit_hash }}, release {{.release_timestamp}})
failed:    :x: *{{.app_name}}* deploy to *{{.stage}}* failed: {{.error}}
rollback:  :leftwards_arrow_with_hook: *{{.app_name}}* on *{{.stage}}* rolled back
```

The succeeded/failed messages get the deploy duration appended automatically (` in 42s`). The start message can't
reference `{{.commit_hash}}` - it is resolved later, at `deploy:updating`.

> **Escaping caveat:** like every `params:` value, a `message_*` override is *also* template-rendered once at load,
> where runtime keys render **empty**. Escape them so they survive to run time:
>
> ```yaml
> message_fail: '{{ "{{ .app_name }} broke on {{ .stage }}: {{ .error }}" }}'
> ```

### Changelog

With `changelog.enabled: true` the success notification also posts what changed: the commits between the previously
deployed revision and the new one (whoosh core's `{{.changelog}}` deploy-context value, captured from the repo mirror
at `deploy:updating`), one attachment per commit - author, subject linked to the commit, and an optional `@mention`
when the author's email is mapped to a Slack member ID:

```yaml
plugins:
  - name: slack
    params:
      webhook_url: '{{ env "SLACK_WEBHOOK_URL" }}'
      rich_fields: true
      changelog:
        enabled: true
        max_commits: 20                # default 20, capped at 100
        # commit_url: ""               # optional: a prefix the SHA is appended to, or a "{hash}" template;
                                       # empty derives https://<host>/<org>/<repo>/commit/ from app.repo
        authors:                       # commit author email -> Slack member ID (mentioned on the commit)
          alice@example.com: U0123ABCD
          bob@example.com: U0456EFGH
```

Notes:

- The commits come from the core `{{.changelog}}` value, so the plugin runs no git itself - and the changelog is
  empty on the first deploy, when both revisions match, outside a deploy, and on a whoosh core without the
  `changelog` context key.
- Redeploying the revision that is already live posts the summary with an explicit "No changes since the previous
  release" note; the other empty cases post the plain summary.
- `max_commits` caps how many of the captured commits are displayed (core captures up to 100).
- Slack limits a message to 20 attachments: the summary plus the first 19 commits go in one message, the rest follow
  as continuation messages.
- Mentions render in each commit's text line - Slack does not render mrkdwn inside attachment titles.
- Everything is best-effort: whatever goes wrong, the plain summary is posted and the deploy never fails because of
  the changelog.

## The `slack:send` action

Post a custom message from any task or hook:

```yaml
tasks:
  announce-migrations:
    action: slack:send
    with:
      message: "Running migrations on *{{.stage}}* ({{ trunc 7 .commit_hash }})"
      color: warning
      optional: true            # don't fail the deploy if Slack is down

hooks:
  before:
    deploy:publishing: [announce-migrations]
```

`with:` fields (all except `message` optional):

| Field | Description |
|---|---|
| `message` | The text to post (**required**). Rendered with the full deploy context; Slack mrkdwn works. |
| `color` | Attachment bar: `good`, `warning`, `danger`, or `#rrggbb`. Empty = no bar. |
| `optional` | `true` makes delivery failures non-fatal (warned instead of failing the task). |
| `webhook_url` | Rewrite the webhook for this task only (beats the per-event and global params) - e.g. post to another workspace/channel's webhook. |
| `channel`, `username`, `icon_emoji` | Per-call overrides of the plugin params (legacy webhooks only). |

A literal `with: webhook_url` value is registered as a masked secret at load, so it is redacted even in `--dry-run`
plans. For a templated value, read it with `envSecret` so it is masked from the moment it renders:

```yaml
with:
  webhook_url: '{{ envSecret "SLACK_TASK_WEBHOOK" }}'
```

A plain `slack:send` task **fails on delivery errors** (so a notification you explicitly asked for isn't silently
lost); set `optional: true` for fire-and-forget. Actions run operator-side - nothing is executed on the hosts. Under
`--dry-run` the action prints its plan and posts nothing.

## Notes

- **Reserved task names:** the plugin contributes hidden tasks `slack:notify-start`, `slack:notify-success`,
  `slack:notify-fail`, `slack:notify-rollback` - a user task with one of these names would be overwritten.
- **Masking:** the webhook URL is registered as a secret (on top of the built-in `hooks.slack.com` pattern), so it is
  redacted from echoed commands, output, logs, and dry-run plans; errors never embed the URL.
- **Per-stage gating:** like any plugin, use `only:`/`except:`/`enabled:` on the spec to notify from some stages only.
- `whoosh <stage> validate` is offline and never contacts Slack; `config`/`run`/`deploy` do load the plugin, so a
  missing `webhook_url` (e.g. unset env var) fails fast with a pointer to the fix.
