// Package slack provides the `slack` whoosh plugin: it posts deploy notifications to a Slack incoming webhook and
// exposes a `slack:send` action any task can invoke for a custom message.
//
// It is its own module (not in the default binary) - build a binary that includes it with:
//
//	whoosh build --with github.com/yousysadmin/whoosh/plugins/slack
//
// Once built in, listing the plugin is enough for the automatic notifications - its startup hook contributes hidden action tasks
// and wires them to the lifecycle: deploy started (before deploy:starting), succeeded (after deploy:finished),
// failed (after deploy:failed; registering that hook is what makes the failure hook fire at all), and - opt-in -
// rolled back (after deploy:rollback). The automatic notifications are best-effort: a Slack outage is logged as a
// warning and never fails the deploy. A user's own `slack:send` task fails on delivery errors unless it sets
// `optional: true`.
//
// Example:
//
//	plugins:
//	  - name: slack
//	    params:
//	      webhook_url: '{{ env "SLACK_WEBHOOK_URL" }}'  # required
//	      channel: "#deploys"                           # optional; legacy webhooks only
//	      notify_rollback: true                         # off by default
//
//	tasks:
//	  announce-migrations:
//	    action: slack:send
//	    with:
//	      message: "Running migrations on *{{.stage}}* ({{ trunc 7 .commit_hash }})"
//	      color: warning
//	      optional: true
//
// Message templates render at action run time with the full deploy context ({{.app_name}}, {{.stage}},
// {{.commit_hash}}, {{.error}}, ...). A message_* override set under params: is ALSO template-rendered once at load
// (where runtime keys render empty), so runtime keys there must be escaped to survive the load render:
// message_fail: '{{ "{{ .error }}" }} ...'. The default messages avoid this by being injected after load.
package slack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName    = "slack"
	pluginVersion = "1.0.0"
	// actionSend is the plugin's single action; its "slack" namespace prefix must match pluginName so the executor's
	// stage-skip logic (SkippedPlugins) applies to it.
	actionSend = "slack:send"

	// Names of the contributed notification tasks (reserved: AddTask would overwrite a same-named user task).
	taskNotifyStart    = "slack:notify-start"
	taskNotifySuccess  = "slack:notify-success"
	taskNotifyFail     = "slack:notify-fail"
	taskNotifyRollback = "slack:notify-rollback"

	defaultTimeout = 10 * time.Second
)

// Default per-event messages. They are placed raw into the contributed tasks' with: maps at startup - after the
// load-time params render - so they are templated exactly once, at action run time, against the full deploy context.
// The start message deliberately omits {{.commit_hash}}: it is resolved only at deploy:updating.
const (
	defaultMsgStart    = ":rocket: *{{.app_name}}* deploy to *{{.stage}}* started (release {{.release_timestamp}})"
	defaultMsgSuccess  = ":white_check_mark: *{{.app_name}}* deployed to *{{.stage}}* ({{ trunc 7 .commit_hash }}, release {{.release_timestamp}})"
	defaultMsgFail     = ":x: *{{.app_name}}* deploy to *{{.stage}}* failed: {{.error}}"
	defaultMsgRollback = ":leftwards_arrow_with_hook: *{{.app_name}}* on *{{.stage}}* rolled back"
)

// Per-event attachment colors: Slack's named colors for the outcomes, a neutral blue for the start announcement.
const (
	colorStart    = "#439FE0"
	colorSuccess  = "good"
	colorFail     = "danger"
	colorRollback = "warning"
)

func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &plugin{} })
}

type plugin struct{}

// Version reports the plugin's version (whoosh.Versioner), shown by `whoosh plugins` / `whoosh version`.
func (p *plugin) Version() string { return pluginVersion }

// params is the plugin's Deployfile `params:` block. Values are template-rendered at load (sprig included), so
// webhook_url is typically '{{ env "SLACK_WEBHOOK_URL" }}'.
type params struct {
	// WebhookURL is the Slack incoming-webhook URL. Required; the default for every notification.
	WebhookURL string `yaml:"webhook_url"`
	// Per-event webhook overrides, so e.g. failures can go to an alerts channel while the rest use the default.
	// They are resolved notifier-side (never placed in the tasks' with: maps), so they stay out of dry-run plans.
	WebhookStart    string `yaml:"webhook_start"`
	WebhookSuccess  string `yaml:"webhook_success"`
	WebhookFail     string `yaml:"webhook_fail"`
	WebhookRollback string `yaml:"webhook_rollback"`
	// Channel/Username/IconEmoji override the webhook's defaults. Honored by legacy incoming webhooks only - Slack-app
	// webhooks ignore them.
	Channel   string `yaml:"channel"`
	Username  string `yaml:"username"`
	IconEmoji string `yaml:"icon_emoji"`

	// Event toggles (default true); rollback notification is opt-in.
	NotifyStart    *bool `yaml:"notify_start"`
	NotifySuccess  *bool `yaml:"notify_success"`
	NotifyFail     *bool `yaml:"notify_fail"`
	NotifyRollback bool  `yaml:"notify_rollback"`

	// Message templates overriding the per-event defaults. Rendered at action run time with the full deploy context,
	// but ALSO rendered once at load like every params: value - escape runtime keys ('{{ "{{ .error }}" }}') so they
	// survive to run time.
	MessageStart    string `yaml:"message_start"`
	MessageSuccess  string `yaml:"message_success"`
	MessageFail     string `yaml:"message_fail"`
	MessageRollback string `yaml:"message_rollback"`

	// Timeout bounds each webhook POST (Go duration string, default "10s").
	Timeout string `yaml:"timeout"`
}

// notifier carries the resolved config plus the state shared between the startup timer hook and the send action: the
// same instance backs both, so the action can report the deploy duration.
type notifier struct {
	cfg       params
	client    *http.Client
	now       func() time.Time // injectable in tests
	startedAt time.Time        // recorded by the before-deploy:starting func-hook; zero outside a deploy
}

// Configure decodes and validates the params, then registers the slack:send action and the startup hook that wires
// the notification tasks.
func (p *plugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var pr params
	if err := whoosh.DecodeParams(spec.Params, &pr); err != nil {
		return fmt.Errorf("slack params: %w", err)
	}
	if pr.WebhookURL == "" {
		return fmt.Errorf("slack: webhook_url is required (e.g. webhook_url: '{{ env \"SLACK_WEBHOOK_URL\" }}' with SLACK_WEBHOOK_URL exported)")
	}
	if err := validateWebhookURL("webhook_url", pr.WebhookURL); err != nil {
		return err
	}
	// The plugin has no per-feature actions: config lives entirely in params, so any actions: entry is a mistake.
	for _, a := range spec.Actions {
		return fmt.Errorf("slack: unknown feature %q under actions: (the plugin is configured via params: only)", a.Name)
	}
	timeout := defaultTimeout
	if pr.Timeout != "" {
		d, err := time.ParseDuration(pr.Timeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("slack: invalid timeout %q (want a positive Go duration, e.g. \"10s\")", pr.Timeout)
		}
		timeout = d
	}
	for _, e := range []struct{ name, url string }{
		{"webhook_start", pr.WebhookStart},
		{"webhook_success", pr.WebhookSuccess},
		{"webhook_fail", pr.WebhookFail},
		{"webhook_rollback", pr.WebhookRollback},
	} {
		if e.url == "" {
			continue
		}
		if err := validateWebhookURL(e.name, e.url); err != nil {
			return err
		}
		whoosh.AddSecret(e.url)
	}
	// The built-in masking rules already cover hooks.slack.com URLs; register the literal too so a proxied or
	// non-standard webhook URL is redacted as well.
	whoosh.AddSecret(pr.WebhookURL)

	n := &notifier{cfg: pr, client: &http.Client{Timeout: timeout}, now: time.Now}
	if err := reg.AddAction(actionSend, n.send); err != nil {
		return err
	}
	reg.AddStartup(n.install)
	return nil
}

// validateWebhookURL checks raw is an absolute http(s) URL. The error names the field, never the value - it may be a
// secret.
func validateWebhookURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("slack: %s is not a valid http(s) URL", field)
	}
	return nil
}

// install wires the notifications: a func-hook before deploy:starting records the start time (always, so duration is
// available even with notify_start off), and each enabled event gets a hidden slack:send task hooked to its phase.
func (n *notifier) install(_ context.Context, cfg *whoosh.DeployFile) error {
	cfg.AddHookFuncBefore(whoosh.PhaseStarting, func(context.Context, io.Writer) error {
		n.startedAt = n.now()
		return nil
	})

	// Register literal task-level webhook overrides (with: webhook_url) as secrets now, at load - the action's own
	// AddSecret only runs when it executes, which a --dry-run plan (printing the with: map) never reaches. Templated
	// values are skipped: they resolve only at run time and are registered then.
	for _, t := range cfg.Tasks {
		if t == nil || t.Action != actionSend {
			continue
		}
		if wu, ok := t.With["webhook_url"].(string); ok && wu != "" && !strings.Contains(wu, "{{") {
			whoosh.AddSecret(wu)
		}
	}

	add := func(name, desc, event, message, color string, phase string, before bool) {
		cfg.AddTask(name, &whoosh.Task{
			Desc:   desc,
			Hidden: true,
			Silent: true,
			Action: actionSend,
			With: map[string]any{
				"event":   event,
				"message": message,
				"color":   color,
			},
		})
		if before {
			cfg.AddHookBefore(phase, name)
		} else {
			cfg.AddHookAfter(phase, name)
		}
	}

	if boolOr(n.cfg.NotifyStart, true) {
		add(taskNotifyStart, "Notify Slack: deploy started",
			eventStarted, def(n.cfg.MessageStart, defaultMsgStart), colorStart, whoosh.PhaseStarting, true)
	}
	if boolOr(n.cfg.NotifySuccess, true) {
		add(taskNotifySuccess, "Notify Slack: deploy succeeded",
			eventFinished, def(n.cfg.MessageSuccess, defaultMsgSuccess), colorSuccess, whoosh.PhaseFinished, false)
	}
	if boolOr(n.cfg.NotifyFail, true) {
		add(taskNotifyFail, "Notify Slack: deploy failed",
			eventFailed, def(n.cfg.MessageFail, defaultMsgFail), colorFail, whoosh.PhaseFailed, false)
	}
	if n.cfg.NotifyRollback {
		add(taskNotifyRollback, "Notify Slack: rollback",
			eventRollback, def(n.cfg.MessageRollback, defaultMsgRollback), colorRollback, whoosh.PhaseRollback, false)
	}
	return nil
}

// def returns v when non-empty, otherwise fallback.
func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// boolOr returns *p, or fallback when p is nil.
func boolOr(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}
