# 06 - Slack notifications

Send a Slack message from a deploy hook.
**One** `local: true` task POSTs to a Slack [incoming webhook][webhook], and a single templated script branches on the
deploy phase, so the same task handles **start, success, and failure**.

[webhook]: https://api.slack.com/messaging/webhooks

> **Zero-script alternative:** the [slack plugin](../../plugins/slack/README.md) (own module; add it with
> `whoosh build --with github.com/yousysadmin/whoosh/plugins/slack`) wires the start/success/failure (and opt-in
> rollback) notifications for you - `plugins: [{name: slack, params: {webhook_url: '{{ env "SLACK_WEBHOOK_URL"
> }}'}}]` - plus a `slack:send` action for custom messages, a structured success/fail message (`rich_fields`), and a
> commit changelog with author mentions (`changelog`). This example stays useful with the stock binary, as a
> template for scripting your own notifier (a different chat service, custom payloads), and for the `{{.phase}}`
> branching pattern.

## Files

```
Deployfile.yml                    # env (webhook), one `notify` task, hooks (3 phases)
deploy/local.yml                  # a local target so `deploy` runs end-to-end
deploy/scripts/slack-notify.sh.tmpl   # templated: branches on {{.phase}}
```

## Run

```sh
export SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../xxxxxxxx

whoosh local notify             # post one message now (phase unset -> "deployed")
whoosh local notify --dry-run   # print the command - webhook URL is redacted
whoosh local deploy             # posts "started", then "deployed" (or "FAILED")
```

## One script, three messages

whoosh exposes the current deploy phase to hook tasks as `{{.phase}}` (template) and `$DEPLOY_PHASE` (env).
The script is a `.tmpl`, so it branches at render time:

```gotemplate
{{- if eq .phase "deploy:starting" }}   status="started ..."
{{- else if eq .phase "deploy:failed" }} status="FAILED ..."  detail={{ .error | quote }}
{{- else }}                              status="deployed ..."
{{- end }}
```

The same `notify` task is wired to three hook points, and whoosh sets the phase for each invocation:

| Hook | `{{.phase}}` | Message |
|---|---|---|
| `after: deploy:starting` | `deploy:starting` | started |
| `after: deploy:finished` | `deploy:finished` | deployed |
| `after: deploy:failed` | `deploy:failed` | FAILED (+ `{{.error}}`) |

`deploy:failed` is special: it isn't a lifecycle step but a hook key whose tasks run when the deploy **errors**, with
the failure message available as `{{.error}}` / `$DEPLOY_ERROR`.
A standalone `whoosh <stage> notify` has no phase, so it falls through to the "deployed" branch.

## How it works

- **Webhook as a secret** - `env.SLACK_WEBHOOK_URL: '{{ env "SLACK_WEBHOOK_URL" }}'` reads the URL from the operator's
  / CI environment at run time, so it is never written in the Deployfile.
- **Templating + env** - the script branches with Go templates (`{{.phase}}`, `{{.error}}`) and fills the message from
  environment variables (`$APP_NAME`, `$STAGE`, `$RELEASE_TIMESTAMP`, `$COMMIT_HASH`) that whoosh exports.
- **Local execution** - `local: true`, so the POST happens from the machine running whoosh, not the target hosts.
- **Redaction** - Slack webhook URLs are a known secret format, scrubbed from command output, the verbose log, and
  `--dry-run` plans (try the dry-run above: the URL shows as `[REDACTED]`).

## Adapting it

- **Slack Web API instead of a webhook**: set `SLACK_BOT_TOKEN` in `env:` and POST to
  `https://slack.com/api/chat.postMessage` with `Authorization: Bearer`.
- **Reuse across stages**: keep this script in your real `deploy/scripts/` and add the `notify` task + the three hooks
  to your shared `Deployfile.yml`.
