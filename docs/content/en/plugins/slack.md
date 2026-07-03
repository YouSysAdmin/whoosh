---
title: "slack"
description: "The Slack plugin - send notification to Slack via WebHook."
weight: 30
---

Notifications to a Slack via [Slack incoming webhook](https://api.slack.com/messaging/webhooks)

## Build with Slack plugin

```sh
whoosh build --with github.com/yousysadmin/whoosh/plugins/slack -o ./whoosh
```

(see [Installation -> With custom plugins](/installation/custom-plugins/)) - then list it under `plugins:`.

## Usage

```yaml
plugins:
  - name: slack
    params:
      webhook_url: '{{ env "SLACK_WEBHOOK_URL" }}'   # required
      channel: "#deploys"                            # optional, legacy webhooks only
      notify_rollback: true                          # off by default
```

With just that, every `whoosh <stage> deploy` posts:

- **started** (blue, before `deploy:starting`) - app, stage, release id
- **succeeded** (green, after `deploy:finished`) - app, stage, commit, release id, duration
- **failed** (red, after `deploy:failed`) - app, stage, the failure message, duration
- **rolled back** (yellow, after `deploy:rollback`) - opt-in via `notify_rollback: true`

Notifications fire only during the deploy lifecycle (never for `config`, `run`, or standalone tasks) and are
**best-effort**: a Slack outage is logged as a warning and never fails (or "un-succeeds") a deploy.

### Params

| Param                                                                     | Default          | Description                                                                                                                           |
|---------------------------------------------------------------------------|------------------|---------------------------------------------------------------------------------------------------------------------------------------|
| `webhook_url`                                                             | - (**required**) | Incoming-webhook URL, the default for every notification. Registered as a masked secret, typically `'{{ env "SLACK_WEBHOOK_URL" }}'`. |
| `webhook_start` / `webhook_success` / `webhook_fail` / `webhook_rollback` | `webhook_url`    | Per-event webhook overrides - e.g. route failures to an alerts channel's webhook while everything else uses the default.              |
| `channel`                                                                 | webhook default  | Override the target channel. Honored by **legacy** webhooks only - Slack-app webhooks ignore it.                                      |
| `username`                                                                | webhook default  | Override the sender name (legacy webhooks only).                                                                                      |
| `icon_emoji`                                                              | webhook default  | Override the sender icon, e.g. `":package:"` (legacy webhooks only).                                                                  |
| `notify_start`                                                            | `true`           | Post when a deploy starts.                                                                                                            |
| `notify_success`                                                          | `true`           | Post when a deploy finishes successfully.                                                                                             |
| `notify_fail`                                                             | `true`           | Post when a deploy fails (this is also what enables the `deploy:failed` hook).                                                        |
| `notify_rollback`                                                         | `false`          | Post after `whoosh <stage> deploy:rollback`.                                                                                          |
| `message_start` / `message_success` / `message_fail` / `message_rollback` | built-in         | Per-event message template overrides (see below).                                                                                     |
| `timeout`                                                                 | `"10s"`          | Bound on each webhook POST (Go duration).                                                                                             |

#### Message templates

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

### Custom message

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
    deploy:publishing: [ announce-migrations ]
```

`with:` fields (all except `message` optional):

| Field                               | Description                                                                                                                        |
|-------------------------------------|------------------------------------------------------------------------------------------------------------------------------------|
| `message`                           | The text to post (**required**). Rendered with the full deploy context, Slack mrkdwn works.                                        |
| `color`                             | Attachment bar: `good`, `warning`, `danger`, or `#rrggbb`. Empty = no bar.                                                         |
| `optional`                          | `true` makes delivery failures non-fatal (warned instead of failing the task).                                                     |
| `webhook_url`                       | Rewrite the webhook for this task only (beats the per-event and global params) - e.g. post to another workspace/channel's webhook. |
| `channel`, `username`, `icon_emoji` | Per-call overrides of the plugin params (legacy webhooks only).                                                                    |

A literal `with: webhook_url` value is registered as a masked secret at load, so it is redacted even in `--dry-run`
plans. For a templated value, read it with `envSecret` so it is masked from the moment it renders:

```yaml
with:
  webhook_url: '{{ envSecret "SLACK_TASK_WEBHOOK" }}'
```

A plain `slack:send` task **fails on delivery errors** (so a notification you explicitly asked for isn't silently
lost), set `optional: true` for fire-and-forget. Actions run operator-side - nothing is executed on the hosts. Under
`--dry-run` the action prints its plan and posts nothing.

### Notes

- **Reserved task names:** the plugin contributes hidden tasks `slack:notify-start`, `slack:notify-success`,
  `slack:notify-fail`, `slack:notify-rollback` - your task with one of these names would be overwritten.
- **Masking:** the webhook URL is registered as a secret (on top of the built-in `hooks.slack.com` pattern), so it is
  redacted from echoed commands, output, logs, and dry-run plans, errors never embed the URL.
- **Per-stage gating:** like any plugin, use `only:`/`except:`/`enabled:` on the spec to notify from some stages only.
- `whoosh <stage> validate` is offline and never contacts Slack, `config`/`run`/`deploy` do load the plugin, so a
  missing `webhook_url` (e.g. unset env var) fails fast with a pointer to the fix.
