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

	// RichFields switches the outcome events to a structured attachment (User/Stage/Branch/Revision/Duration/Release
	// fields). Set by install() from the rich_fields param, together with the deploy-context values below - the
	// action only sees its with: map, so the context is injected there as runtime templates.
	RichFields  bool   `yaml:"rich_fields"`
	Stage       string `yaml:"stage"`
	Branch      string `yaml:"branch"`
	ReleasePath string `yaml:"release_path"`
	CommitHash  string `yaml:"commit_hash"`
	Deployer    string `yaml:"deployer"`

	// Changelog inputs, injected by install() when changelog.enabled: whoosh core's captured commit list
	// (sha|author|email|subject lines), the repo remote for deriving commit links, and the previous revision -
	// which, together with CommitHash, tells an unchanged redeploy apart from a first deploy.
	Changelog          string `yaml:"changelog"`
	Repo               string `yaml:"repo"`
	PreviousCommitHash string `yaml:"previous_commit_hash"`
}

// payload is the incoming-webhook JSON body. Channel/username/icon overrides are honored by legacy webhooks only.
type payload struct {
	Channel     string       `json:"channel,omitempty"`
	Username    string       `json:"username,omitempty"`
	IconEmoji   string       `json:"icon_emoji,omitempty"`
	Attachments []attachment `json:"attachments"`
}

// attachment is the message body: one colored bar + mrkdwn text (named colors work only on attachments).
// Title/TitleLink/AuthorName/Fields are used by the rich outcome message and the changelog entries.
type attachment struct {
	Color      string        `json:"color,omitempty"`
	Text       string        `json:"text,omitempty"`
	Fallback   string        `json:"fallback,omitempty"`
	Title      string        `json:"title,omitempty"`
	TitleLink  string        `json:"title_link,omitempty"`
	AuthorName string        `json:"author_name,omitempty"`
	Fields     []attachField `json:"fields,omitempty"`
	MrkdwnIn   []string      `json:"mrkdwn_in,omitempty"`
}

// attachField is one entry of an attachment's fields table. Short fields render two per row.
type attachField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
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
	outcome := sp.Event == eventFinished || sp.Event == eventFailed
	// Outcome notifications report how long the deploy took; startedAt is zero outside the deploy lifecycle
	// (standalone task runs, rollback without a prior deploy in this process), where the suffix is omitted.
	// With rich_fields the duration moves into the fields table instead.
	if outcome && !sp.RichFields && !n.startedAt.IsZero() {
		msg += fmt.Sprintf(" in %s", n.now().Sub(n.startedAt).Round(time.Second))
	}

	// The changelog rides the success notification: the summary attachment heads the first message, commits fill it
	// up to Slack's 20-attachment limit, the rest go out as continuation messages. The commit list comes from whoosh
	// core's {{.changelog}} value. An unchanged redeploy gets an explicit note (parsed before the attachment so the
	// note lands in the summary text), while a first deploy or an older core stays a plain summary.
	var commits []commit
	if sp.Event == eventFinished && n.cfg.Changelog.Enabled {
		commits = parseCommits(sp.Changelog, n.changelogMax())
		if len(commits) == 0 {
			if isCommitSHA(sp.PreviousCommitHash) && sp.PreviousCommitHash == sp.CommitHash {
				msg += "\n_No changes since the previous release._"
				slog.Debug("slack: changelog empty, revisions match")
			} else {
				slog.Debug("slack: changelog empty")
			}
		}
	}

	att := attachment{
		Color:    sp.Color,
		Text:     msg,
		Fallback: msg,
		MrkdwnIn: []string{"text"},
	}
	if outcome && sp.RichFields {
		att.Fields = n.richFields(ctx, sp)
	}

	batches := [][]attachment{{att}}
	if len(commits) > 0 {
		atts := changelogAttachments(commits, n.commitURLBase(sp.Repo), n.cfg.Changelog.Authors)
		batches = batchAttachments(att, atts, maxAttachmentsPerMessage)
	}

	pl := payload{
		Channel:     def(sp.Channel, n.cfg.Channel),
		Username:    def(sp.Username, n.cfg.Username),
		IconEmoji:   def(sp.IconEmoji, n.cfg.IconEmoji),
		Attachments: batches[0],
	}

	err := n.post(ctx, webhook, pl)
	if err == nil {
		// Changelog continuations are best-effort like the summary: a failure warns and stops the sequence.
		for _, b := range batches[1:] {
			cont := pl
			cont.Attachments = b
			if perr := n.post(ctx, webhook, cont); perr != nil {
				slog.Warn("slack: changelog continuation failed", "event", sp.Event, "error", perr)
				break
			}
		}
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

// richFields builds the structured outcome attachment fields from the deploy context injected into the task's with:
// map. Empty values are omitted (e.g. Revision when the failure predates deploy:updating).
func (n *notifier) richFields(ctx context.Context, sp sendParams) []attachField {
	var fields []attachField
	add := func(title, value string) {
		if value != "" {
			fields = append(fields, attachField{Title: title, Value: value, Short: true})
		}
	}
	add("User", n.displayDeployer(ctx, sp.Deployer))
	add("Stage", sp.Stage)
	add("Branch", sp.Branch)
	rev := sp.CommitHash
	if len(rev) > 7 {
		rev = rev[:7]
	}
	add("Revision", rev)
	if !n.startedAt.IsZero() {
		add("Duration", n.now().Sub(n.startedAt).Round(time.Second).String())
	}
	add("Release", sp.ReleasePath)
	return fields
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
