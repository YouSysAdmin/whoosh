package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yousysadmin/whoosh"
)

// Events stamped by the plugin's own notification tasks. A non-empty event makes delivery best-effort and, for the
// outcome events, appends the deploy duration measured from the startup timer hook.
const (
	eventStarted  = "started"
	eventFinished = "finished"
	eventFailed   = "failed"
	eventRollback = "rollback"
)

// sendParams is the `with:` contract of the slack:send action. The executor has already template-rendered the values
// against the full deploy context by the time they arrive here.
type sendParams struct {
	// Message is the text to post. Required. Slack mrkdwn (*bold*, :emoji:, <links>) is honored.
	Message string `yaml:"message"`
	// Color is the attachment bar: "good", "warning", "danger", or "#rrggbb". Empty posts without a color bar.
	Color string `yaml:"color"`

	// Per-call overrides of the plugin params.
	WebhookURL string `yaml:"webhook_url"`
	Channel    string `yaml:"channel"`
	Username   string `yaml:"username"`
	IconEmoji  string `yaml:"icon_emoji"`

	// Optional makes a delivery failure non-fatal: logged as a warning instead of failing the task.
	Optional bool `yaml:"optional"`

	// Event marks the plugin's own notification tasks ("started"/"finished"/"failed"/"rollback"); it implies
	// best-effort delivery and adds the deploy duration for the outcome events. Not meant for user tasks.
	Event string `yaml:"event"`
}

// payload is the incoming-webhook JSON body. Channel/username/icon overrides are honored by legacy webhooks only.
type payload struct {
	Channel     string       `json:"channel,omitempty"`
	Username    string       `json:"username,omitempty"`
	IconEmoji   string       `json:"icon_emoji,omitempty"`
	Attachments []attachment `json:"attachments"`
}

// attachment is the message body: one colored bar + mrkdwn text (named colors work only on attachments).
type attachment struct {
	Color    string   `json:"color,omitempty"`
	Text     string   `json:"text"`
	Fallback string   `json:"fallback,omitempty"`
	MrkdwnIn []string `json:"mrkdwn_in,omitempty"`
}

// send is the slack:send action. Delivery failures are swallowed (warned) for the plugin's own notification tasks
// (event set) and for user tasks opting in via optional: true; otherwise they fail the task.
// All narrative goes through slog (the whoosh convention); the console writer is unused - the action produces no
// command output.
func (n *notifier) send(ctx context.Context, raw map[string]any, _ io.Writer) error {
	var sp sendParams
	if err := whoosh.DecodeParams(raw, &sp); err != nil {
		return fmt.Errorf("slack:send params: %w", err)
	}
	if strings.TrimSpace(sp.Message) == "" {
		return fmt.Errorf("slack:send: 'message' is required")
	}

	webhook := n.webhookFor(sp.Event)
	if sp.WebhookURL != "" {
		if err := validateWebhookURL("with: webhook_url", sp.WebhookURL); err != nil {
			return err
		}
		whoosh.AddSecret(sp.WebhookURL)
		webhook = sp.WebhookURL
	}

	msg := sp.Message
	// Outcome notifications report how long the deploy took; startedAt is zero outside the deploy lifecycle
	// (standalone task runs, rollback without a prior deploy in this process), where the suffix is omitted.
	if (sp.Event == eventFinished || sp.Event == eventFailed) && !n.startedAt.IsZero() {
		msg += fmt.Sprintf(" in %s", n.now().Sub(n.startedAt).Round(time.Second))
	}

	pl := payload{
		Channel:   def(sp.Channel, n.cfg.Channel),
		Username:  def(sp.Username, n.cfg.Username),
		IconEmoji: def(sp.IconEmoji, n.cfg.IconEmoji),
		Attachments: []attachment{{
			Color:    sp.Color,
			Text:     msg,
			Fallback: msg,
			MrkdwnIn: []string{"text"},
		}},
	}

	err := n.post(ctx, webhook, pl)
	if err == nil {
		slog.Info("slack: notification sent", "event", sp.Event)
		return nil
	}
	// Best-effort paths: the plugin's own notifications must never fail a deploy (a failing after-deploy:finished
	// hook would cascade into a false deploy:failed), and optional: true extends that to user tasks.
	if sp.Event != "" || sp.Optional {
		slog.Warn("slack: notification failed", "event", sp.Event, "error", err)
		return nil
	}
	return fmt.Errorf("slack:send: %w", err)
}

// webhookFor resolves the webhook for an event: its per-event override param when set, else the global webhook_url.
// Kept notifier-side (not in the tasks' with: maps) so the URLs never surface in dry-run plans or rendered params.
func (n *notifier) webhookFor(event string) string {
	byEvent := map[string]string{
		eventStarted:  n.cfg.WebhookStart,
		eventFinished: n.cfg.WebhookSuccess,
		eventFailed:   n.cfg.WebhookFail,
		eventRollback: n.cfg.WebhookRollback,
	}
	return def(byEvent[event], n.cfg.WebhookURL)
}

// post POSTs the payload to the webhook. The client's Timeout bounds the call even when ctx has no deadline (the
// deploy:failed hook runs on context.Background()). Errors never embed the URL - it may be a secret.
func (n *notifier) post(ctx context.Context, webhook string, pl payload) error {
	body, err := json.Marshal(pl)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		// A *url.Error embeds the full URL in its message; unwrap it so the webhook (a secret) never reaches logs.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("post to webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Slack replies with short plain-text reasons ("invalid_payload", "no_service", ...).
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
