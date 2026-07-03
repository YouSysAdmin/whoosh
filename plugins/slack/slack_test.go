package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/yousysadmin/whoosh"
)

// webhookServer is an httptest stub capturing every posted payload.
type webhookServer struct {
	*httptest.Server
	mu       sync.Mutex
	payloads []payload
	headers  []http.Header
	status   int
	body     string
}

func newWebhookServer(t *testing.T) *webhookServer {
	t.Helper()
	ws := &webhookServer{status: http.StatusOK, body: "ok"}
	ws.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pl payload
		if err := json.NewDecoder(r.Body).Decode(&pl); err != nil {
			t.Errorf("decode webhook body: %v", err)
		}
		ws.mu.Lock()
		ws.payloads = append(ws.payloads, pl)
		ws.headers = append(ws.headers, r.Header.Clone())
		ws.mu.Unlock()
		w.WriteHeader(ws.status)
		io.WriteString(w, ws.body)
	}))
	t.Cleanup(ws.Close)
	return ws
}

func (ws *webhookServer) received() []payload {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]payload(nil), ws.payloads...)
}

// loadSlack configures the plugin with the given params and returns the registry and the send action.
func loadSlack(t *testing.T, params map[string]any) (*whoosh.Registry, whoosh.ActionFunc) {
	t.Helper()
	reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: params}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fn, ok := reg.Action(actionSend)
	if !ok {
		t.Fatalf("action %s not registered", actionSend)
	}
	return reg, fn
}

func TestRegisteredUnderDocumentedName(t *testing.T) {
	if pluginName != "slack" {
		t.Fatalf("pluginName = %q, want %q (the documented name)", pluginName, "slack")
	}
	if !whoosh.IsRegistered(pluginName) {
		t.Fatal("plugin not registered")
	}
}

// The plugin reports a version via whoosh.Versioner, shown by `whoosh plugins` / `whoosh version`.
func TestVersion(t *testing.T) {
	var p whoosh.Plugin = &plugin{}
	v, ok := p.(whoosh.Versioner)
	if !ok {
		t.Fatal("plugin does not implement whoosh.Versioner")
	}
	if v.Version() != pluginVersion || pluginVersion == "" {
		t.Fatalf("Version() = %q, want %q", v.Version(), pluginVersion)
	}
}

func TestConfigure_Validation(t *testing.T) {
	cases := []struct {
		name string
		spec whoosh.PluginSpec
		want string // substring of the expected error
	}{
		{"missing webhook_url", whoosh.PluginSpec{Name: pluginName}, "webhook_url is required"},
		{"invalid webhook_url", whoosh.PluginSpec{Name: pluginName, Params: map[string]any{"webhook_url": "not a url"}}, "not a valid http(s) URL"},
		{"bad timeout", whoosh.PluginSpec{Name: pluginName, Params: map[string]any{"webhook_url": "https://example.com/hook", "timeout": "nope"}}, "invalid timeout"},
		{"unknown actions entry", whoosh.PluginSpec{Name: pluginName,
			Params:  map[string]any{"webhook_url": "https://example.com/hook"},
			Actions: []whoosh.PluginActionSpec{{Name: "slack:bogus"}}}, "unknown feature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := whoosh.Load([]whoosh.PluginSpec{tc.spec})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// Startup wires the timer func-hook plus the three default notification tasks, each hidden and hooked to its phase.
func TestStartup_WiresTasksAndHooks(t *testing.T) {
	srv := newWebhookServer(t)
	reg, _ := loadSlack(t, map[string]any{"webhook_url": srv.URL})
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}

	if n := len(cfg.HookFuncsBefore[whoosh.PhaseStarting]); n != 1 {
		t.Errorf("before %s func hooks = %d, want 1 (the start timer)", whoosh.PhaseStarting, n)
	}

	wired := []struct {
		task   string
		hooked []string
	}{
		{taskNotifyStart, cfg.Hooks.Before[whoosh.PhaseStarting]},
		{taskNotifySuccess, cfg.Hooks.After[whoosh.PhaseFinished]},
		{taskNotifyFail, cfg.Hooks.After[whoosh.PhaseFailed]},
	}
	for _, w := range wired {
		task, ok := cfg.Tasks[w.task]
		if !ok {
			t.Errorf("task %s not contributed", w.task)
			continue
		}
		if !task.Hidden || !task.Silent || task.Action != actionSend {
			t.Errorf("task %s = %+v, want hidden+silent action %s", w.task, task, actionSend)
		}
		if task.With["message"] == "" || task.With["event"] == "" {
			t.Errorf("task %s with = %v, want message and event set", w.task, task.With)
		}
		if len(w.hooked) != 1 || w.hooked[0] != w.task {
			t.Errorf("hook wiring for %s = %v", w.task, w.hooked)
		}
	}
	if _, ok := cfg.Tasks[taskNotifyRollback]; ok {
		t.Error("rollback task contributed by default, want opt-in only")
	}
}

func TestStartup_Toggles(t *testing.T) {
	srv := newWebhookServer(t)
	reg, _ := loadSlack(t, map[string]any{
		"webhook_url":     srv.URL,
		"notify_start":    false,
		"notify_rollback": true,
	})
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if _, ok := cfg.Tasks[taskNotifyStart]; ok {
		t.Error("notify_start: false still contributed the start task")
	}
	if len(cfg.Hooks.Before[whoosh.PhaseStarting]) != 0 {
		t.Errorf("notify_start: false still hooked %v before %s", cfg.Hooks.Before[whoosh.PhaseStarting], whoosh.PhaseStarting)
	}
	if got := cfg.Hooks.After[whoosh.PhaseRollback]; len(got) != 1 || got[0] != taskNotifyRollback {
		t.Errorf("notify_rollback: true hooks = %v, want [%s]", got, taskNotifyRollback)
	}
	// The timer func-hook is registered regardless of notify_start (duration for success/fail).
	if n := len(cfg.HookFuncsBefore[whoosh.PhaseStarting]); n != 1 {
		t.Errorf("timer func hooks = %d, want 1", n)
	}
}

// A message_* override replaces the default template in the contributed task's with:.
func TestStartup_MessageOverride(t *testing.T) {
	srv := newWebhookServer(t)
	reg, _ := loadSlack(t, map[string]any{
		"webhook_url":  srv.URL,
		"message_fail": "custom fail text",
	})
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if got := cfg.Tasks[taskNotifyFail].With["message"]; got != "custom fail text" {
		t.Errorf("fail message = %v, want the override", got)
	}
	if got := cfg.Tasks[taskNotifySuccess].With["message"]; got != defaultMsgSuccess {
		t.Errorf("success message = %v, want the default", got)
	}
}

func TestSend_PostsPayload(t *testing.T) {
	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{
		"webhook_url": srv.URL,
		"channel":     "#deploys",
		"username":    "whoosh",
		"icon_emoji":  ":package:",
	})

	err := send(context.Background(), map[string]any{"message": "hello *world*", "color": "warning"}, io.Discard)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	got := srv.received()
	if len(got) != 1 {
		t.Fatalf("posted %d payloads, want 1", len(got))
	}
	pl := got[0]
	if pl.Channel != "#deploys" || pl.Username != "whoosh" || pl.IconEmoji != ":package:" {
		t.Errorf("payload overrides = %+v", pl)
	}
	if len(pl.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want 1", pl.Attachments)
	}
	a := pl.Attachments[0]
	if a.Text != "hello *world*" || a.Color != "warning" || a.Fallback != a.Text {
		t.Errorf("attachment = %+v", a)
	}
	if ct := srv.headers[0].Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSend_WithOverridesBeatPluginParams(t *testing.T) {
	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{"webhook_url": srv.URL, "channel": "#deploys"})

	if err := send(context.Background(), map[string]any{"message": "m", "channel": "#ops"}, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := srv.received()[0].Channel; got != "#ops" {
		t.Errorf("channel = %q, want the with: override", got)
	}
}

func TestSend_MessageRequired(t *testing.T) {
	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{"webhook_url": srv.URL})
	err := send(context.Background(), map[string]any{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "'message' is required") {
		t.Fatalf("send error = %v, want message-required", err)
	}
	if len(srv.received()) != 0 {
		t.Error("posted despite missing message")
	}
}

// Delivery-failure policy: a plain user send fails the task; optional: true and the plugin's own event sends are
// best-effort (nil).
func TestSend_FailurePolicy(t *testing.T) {
	srv := newWebhookServer(t)
	srv.status = http.StatusInternalServerError
	srv.body = "rollup_error"
	_, send := loadSlack(t, map[string]any{"webhook_url": srv.URL})

	err := send(context.Background(), map[string]any{"message": "m"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "rollup_error") {
		t.Fatalf("plain send error = %v, want the 500 + body", err)
	}
	if err := send(context.Background(), map[string]any{"message": "m", "optional": true}, io.Discard); err != nil {
		t.Fatalf("optional send error = %v, want nil", err)
	}
	if err := send(context.Background(), map[string]any{"message": "m", "event": eventFinished}, io.Discard); err != nil {
		t.Fatalf("event send error = %v, want nil (best-effort)", err)
	}

	// Connection refused (dead endpoint) follows the same policy.
	dead := newWebhookServer(t)
	dead.Close()
	_, sendDead := loadSlack(t, map[string]any{"webhook_url": dead.URL})
	if err := sendDead(context.Background(), map[string]any{"message": "m"}, io.Discard); err == nil {
		t.Fatal("plain send to dead endpoint = nil, want error")
	}
	if err := sendDead(context.Background(), map[string]any{"message": "m", "event": eventFailed}, io.Discard); err != nil {
		t.Fatalf("event send to dead endpoint = %v, want nil (best-effort)", err)
	}
}

// The outcome events append the deploy duration measured by the startup timer hook; a zero start time omits it.
func TestSend_EventDuration(t *testing.T) {
	srv := newWebhookServer(t)
	reg, send := loadSlack(t, map[string]any{"webhook_url": srv.URL})
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}

	// No timer fired yet: no duration suffix.
	if err := send(context.Background(), map[string]any{"message": "done", "event": eventFinished}, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := srv.received()[0].Attachments[0].Text; got != "done" {
		t.Errorf("text = %q, want no duration suffix before the timer hook ran", got)
	}

	// Fire the timer hook, then advance the clock via the shared notifier's now func. The action closure and the timer
	// hook share the notifier instance, so mutate it through the registered hook + a stubbed clock.
	base := time.Now()
	clock := base
	// Reach the notifier: the timer hook closes over it; stub time by swapping now before firing the hook.
	// (in-package test: find the notifier through the action's receiver is not possible, so rebuild one directly)
	n := &notifier{cfg: params{WebhookURL: srv.URL}, client: srv.Client(), now: func() time.Time { return clock }}
	subCfg := &whoosh.DeployFile{}
	if err := n.install(context.Background(), subCfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := subCfg.HookFuncsBefore[whoosh.PhaseStarting][0](context.Background(), io.Discard); err != nil {
		t.Fatalf("timer hook: %v", err)
	}
	clock = base.Add(5 * time.Second)
	if err := n.send(context.Background(), map[string]any{"message": "done", "event": eventFinished}, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := srv.received()
	if text := got[len(got)-1].Attachments[0].Text; text != "done in 5s" {
		t.Errorf("text = %q, want %q", text, "done in 5s")
	}

	// The start event never carries a duration.
	if err := n.send(context.Background(), map[string]any{"message": "go", "event": eventStarted}, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	got = srv.received()
	if text := got[len(got)-1].Attachments[0].Text; text != "go" {
		t.Errorf("start text = %q, want no duration", text)
	}
}

func TestSend_WebhookOverride(t *testing.T) {
	main := newWebhookServer(t)
	other := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{"webhook_url": main.URL})

	if err := send(context.Background(), map[string]any{"message": "m", "webhook_url": other.URL}, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(main.received()) != 0 || len(other.received()) != 1 {
		t.Errorf("main got %d, override got %d; want 0/1", len(main.received()), len(other.received()))
	}

	if err := send(context.Background(), map[string]any{"message": "m", "webhook_url": "::bad::"}, io.Discard); err == nil {
		t.Fatal("invalid webhook_url override accepted")
	}
}

// Per-event webhook params reroute that event's notification; other events keep the global webhook, and a with:
// override still wins over both.
func TestSend_PerEventWebhook(t *testing.T) {
	main := newWebhookServer(t)
	alerts := newWebhookServer(t)
	other := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{
		"webhook_url":  main.URL,
		"webhook_fail": alerts.URL,
	})

	if err := send(context.Background(), map[string]any{"message": "m", "event": eventFailed}, io.Discard); err != nil {
		t.Fatalf("send failed event: %v", err)
	}
	if err := send(context.Background(), map[string]any{"message": "m", "event": eventFinished}, io.Discard); err != nil {
		t.Fatalf("send finished event: %v", err)
	}
	if err := send(context.Background(), map[string]any{"message": "m"}, io.Discard); err != nil {
		t.Fatalf("send plain: %v", err)
	}
	if got := len(alerts.received()); got != 1 {
		t.Errorf("failure webhook got %d payloads, want 1", got)
	}
	if got := len(main.received()); got != 2 {
		t.Errorf("global webhook got %d payloads, want 2 (finished + plain)", got)
	}

	// with: webhook_url beats the per-event param.
	if err := send(context.Background(), map[string]any{"message": "m", "event": eventFailed, "webhook_url": other.URL}, io.Discard); err != nil {
		t.Fatalf("send with override: %v", err)
	}
	if got := len(other.received()); got != 1 {
		t.Errorf("with: override webhook got %d payloads, want 1", got)
	}
}

func TestConfigure_PerEventWebhookValidated(t *testing.T) {
	_, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: map[string]any{
		"webhook_url":  "https://example.com/hook",
		"webhook_fail": "not a url",
	}}})
	if err == nil || !strings.Contains(err.Error(), "webhook_fail") {
		t.Fatalf("Load error = %v, want it to name webhook_fail", err)
	}
}

// The default message templates must render against a populated deploy context (guards template typos; trunc is
// sprig). Mirrors the executor's renderer: text/template + sprig, missingkey=error, lowercase deploy keys.
func TestDefaultTemplatesRender(t *testing.T) {
	data := map[string]any{
		"app_name":          "myapp",
		"stage":             "production",
		"release_timestamp": "20260703120000",
		"commit_hash":       "0123456789abcdef",
		"error":             "boom",
	}
	want := map[string]string{
		defaultMsgStart:    "started (release 20260703120000)",
		defaultMsgSuccess:  "(0123456, release 20260703120000)",
		defaultMsgFail:     "failed: boom",
		defaultMsgRollback: "rolled back",
	}
	for tmpl, sub := range want {
		parsed, err := template.New("msg").Option("missingkey=error").Funcs(sprig.TxtFuncMap()).Parse(tmpl)
		if err != nil {
			t.Errorf("parse %q: %v", tmpl, err)
			continue
		}
		var b strings.Builder
		if err := parsed.Execute(&b, data); err != nil {
			t.Errorf("render %q: %v", tmpl, err)
			continue
		}
		got := b.String()
		if !strings.Contains(got, sub) || !strings.Contains(got, "myapp") || !strings.Contains(got, "production") {
			t.Errorf("rendered %q = %q, want it to contain %q + app + stage", tmpl, got, sub)
		}
	}
}

// A literal task-level webhook override (with: webhook_url) is registered as a secret at startup, so it is redacted
// even in --dry-run plans, where the action never runs.
func TestStartup_MasksTaskLevelWebhook(t *testing.T) {
	srv := newWebhookServer(t)
	reg, _ := loadSlack(t, map[string]any{"webhook_url": srv.URL})
	taskHook := "https://chat-proxy.example.com/hooks/task-level-secret-token"
	cfg := &whoosh.DeployFile{}
	cfg.AddTask("announce", &whoosh.Task{Action: actionSend, With: map[string]any{
		"message":     "m",
		"webhook_url": taskHook,
	}})
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if masked := whoosh.Masking("action slack:send with " + taskHook); strings.Contains(masked, "task-level-secret-token") {
		t.Errorf("task-level webhook not masked at startup: %q", masked)
	}
}

// Configure registers the webhook URL as a masked secret, covering non-hooks.slack.com (proxied) URLs too.
func TestSecretMasked(t *testing.T) {
	secret := "https://slack-proxy.internal.example.com/services/T000/B000/verysecrettoken"
	if _, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: map[string]any{"webhook_url": secret}}}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if masked := whoosh.Masking("posting to " + secret + " now"); strings.Contains(masked, "verysecrettoken") {
		t.Errorf("webhook URL not masked: %q", masked)
	}
}
