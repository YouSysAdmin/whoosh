package slack

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh"
)

func TestDeriveCommitURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:eduvo/mb_rails4.git":        "https://github.com/eduvo/mb_rails4/commit/",
		"git@github.com:eduvo/mb_rails4":            "https://github.com/eduvo/mb_rails4/commit/",
		"ssh://git@github.com/eduvo/mb_rails4.git":  "https://github.com/eduvo/mb_rails4/commit/",
		"ssh://git@gitlab.example.com:2222/org/app": "https://gitlab.example.com/org/app/commit/",
		"https://github.com/eduvo/mb_rails4.git":    "https://github.com/eduvo/mb_rails4/commit/",
		"https://gitlab.example.com/grp/sub/app":    "https://gitlab.example.com/grp/sub/app/commit/",
		"/srv/git/mirror.git":                       "",
		"":                                          "",
		"github.com/eduvo/mb_rails4":                "",
	}
	for repo, want := range cases {
		if got := deriveCommitURL(repo); got != want {
			t.Errorf("deriveCommitURL(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestCommitLink(t *testing.T) {
	if got := commitLink("https://gh.example.com/o/r/commit/", "abc1234"); got != "https://gh.example.com/o/r/commit/abc1234" {
		t.Errorf("prefix link = %q", got)
	}
	if got := commitLink("https://gh.example.com/o/r/-/commit/{hash}?view=parallel", "abc1234"); got != "https://gh.example.com/o/r/-/commit/abc1234?view=parallel" {
		t.Errorf("token link = %q", got)
	}
	if got := commitLink("", "abc1234"); got != "" {
		t.Errorf("empty base link = %q, want empty", got)
	}
}

func TestParseCommits(t *testing.T) {
	out := strings.Join([]string{
		"0f4c1a7d9e2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d|Alice|alice@example.com|fix: handle a|b pipes",
		"",
		"not a sha|Bob|bob@example.com|broken line",
		"aaaaaaaa|Carol|carol@example.com|feat: minimal",
	}, "\n")
	cs := parseCommits(out, 10)
	if len(cs) != 2 {
		t.Fatalf("parsed %d commits, want 2: %+v", len(cs), cs)
	}
	if cs[0].Subject != "fix: handle a|b pipes" || cs[0].Email != "alice@example.com" {
		t.Errorf("commit[0] = %+v (subject must keep its pipes)", cs[0])
	}
	if cs[1].SHA != "aaaaaaaa" || cs[1].Author != "Carol" {
		t.Errorf("commit[1] = %+v", cs[1])
	}
	// max truncates (newest first, so the first lines survive).
	if cs := parseCommits(out, 1); len(cs) != 1 || cs[0].Email != "alice@example.com" {
		t.Errorf("parseCommits(max=1) = %+v, want just the first commit", cs)
	}
}

func TestBatchAttachments(t *testing.T) {
	head := attachment{Title: "summary"}
	rest := make([]attachment, 25)
	batches := batchAttachments(head, rest, 20)
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	if len(batches[0]) != 20 || batches[0][0].Title != "summary" {
		t.Errorf("first batch = %d attachments (head %q), want 20 with the summary first", len(batches[0]), batches[0][0].Title)
	}
	if len(batches[1]) != 6 {
		t.Errorf("second batch = %d attachments, want 6", len(batches[1]))
	}
	if only := batchAttachments(head, nil, 20); len(only) != 1 || len(only[0]) != 1 {
		t.Errorf("no commits should yield the bare summary, got %+v", only)
	}
}

// changelogLines renders n commits in the core {{.changelog}} format, commit 0 authored by alice.
func changelogLines(n int) string {
	var lines []string
	for i := range n {
		email := "bob@example.com"
		if i == 0 {
			email = "alice@example.com" // matched case-insensitively against the normalized authors map
		}
		lines = append(lines, fmt.Sprintf("%040x|Author %d|%s|commit %d", i+1, i, email, i))
	}
	return strings.Join(lines, "\n")
}

func changelogSendWith(changelog string) map[string]any {
	return map[string]any{
		"message":              "done",
		"event":                eventFinished,
		"changelog":            changelog,
		"repo":                 "git@github.com:eduvo/mb_rails4.git",
		"commit_hash":          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"previous_commit_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

// The success notification carries the core changelog value as attachments: summary first, batched at 20 per
// message, mentions only for mapped emails, links from the repo remote.
func TestSend_Changelog(t *testing.T) {
	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{
		"webhook_url": srv.URL,
		"changelog": map[string]any{
			"enabled":     true,
			"max_commits": 30,
			"authors":     map[string]any{"Alice@Example.com": "U0ALICE"},
		},
	})

	if err := send(context.Background(), changelogSendWith(changelogLines(25)), io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := srv.received()
	if len(got) != 2 {
		t.Fatalf("posted %d messages, want 2 (20 + 6 attachments)", len(got))
	}
	first, second := got[0].Attachments, got[1].Attachments
	if len(first) != 20 || len(second) != 6 {
		t.Fatalf("attachments = %d + %d, want 20 + 6", len(first), len(second))
	}
	if first[0].Text != "done" {
		t.Errorf("summary attachment text = %q, want the success message first (and no no-changes note)", first[0].Text)
	}
	c0 := first[1]
	if c0.Title != "commit 0" || c0.AuthorName != "Author 0" {
		t.Errorf("commit attachment = %+v", c0)
	}
	if c0.TitleLink != "https://github.com/eduvo/mb_rails4/commit/"+fmt.Sprintf("%040x", 1) {
		t.Errorf("commit link = %q", c0.TitleLink)
	}
	if c0.Text != "<@U0ALICE>" {
		t.Errorf("mapped author mention = %q, want <@U0ALICE>", c0.Text)
	}
	if first[2].Text != "" {
		t.Errorf("unmapped author got a mention: %q", first[2].Text)
	}
}

// max_commits caps how many of the captured commits are displayed.
func TestSend_ChangelogMaxCommits(t *testing.T) {
	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{
		"webhook_url": srv.URL,
		"changelog":   map[string]any{"enabled": true, "max_commits": 3},
	})
	if err := send(context.Background(), changelogSendWith(changelogLines(10)), io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := srv.received()
	if len(got) != 1 || len(got[0].Attachments) != 4 {
		t.Fatalf("want 1 message with summary + 3 commits, got %+v", got)
	}
}

// An empty or garbage changelog value (fresh deploy, unchanged redeploy, older core) posts the plain summary.
func TestSend_ChangelogDegradesToPlain(t *testing.T) {
	for name, changelog := range map[string]string{
		"empty":   "",
		"garbage": "not|a\nvalid changelog",
	} {
		t.Run(name, func(t *testing.T) {
			srv := newWebhookServer(t)
			_, send := loadSlack(t, map[string]any{
				"webhook_url": srv.URL,
				"changelog":   map[string]any{"enabled": true},
			})
			if err := send(context.Background(), changelogSendWith(changelog), io.Discard); err != nil {
				t.Fatalf("send: %v", err)
			}
			got := srv.received()
			if len(got) != 1 || len(got[0].Attachments) != 1 {
				t.Fatalf("want a single plain message, got %+v", got)
			}
		})
	}
}

// An unchanged redeploy (previous revision equals the new one) gets an explicit no-changes note in the summary,
// while a first deploy (no previous revision) stays a plain summary.
func TestSend_ChangelogNoChangesNote(t *testing.T) {
	const note = "_No changes since the previous release._"

	srv := newWebhookServer(t)
	_, send := loadSlack(t, map[string]any{
		"webhook_url": srv.URL,
		"changelog":   map[string]any{"enabled": true},
	})

	same := changelogSendWith("")
	same["previous_commit_hash"] = same["commit_hash"]
	if err := send(context.Background(), same, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := srv.received()
	if len(got) != 1 || len(got[0].Attachments) != 1 {
		t.Fatalf("want a single message, got %+v", got)
	}
	if text := got[0].Attachments[0].Text; !strings.HasSuffix(text, note) {
		t.Errorf("summary text = %q, want the no-changes note appended", text)
	}

	fresh := changelogSendWith("")
	fresh["previous_commit_hash"] = ""
	if err := send(context.Background(), fresh, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	got = srv.received()
	if text := got[len(got)-1].Attachments[0].Text; strings.Contains(text, note) {
		t.Errorf("first deploy got the no-changes note: %q", text)
	}

	// Changelog disabled: an unchanged redeploy stays a plain summary too.
	srvOff := newWebhookServer(t)
	_, sendOff := loadSlack(t, map[string]any{"webhook_url": srvOff.URL})
	if err := sendOff(context.Background(), same, io.Discard); err != nil {
		t.Fatalf("send: %v", err)
	}
	if text := srvOff.received()[0].Attachments[0].Text; strings.Contains(text, note) {
		t.Errorf("disabled changelog got the no-changes note: %q", text)
	}
}

// Startup injects the changelog's runtime inputs into the success task only.
func TestStartup_ChangelogInjectsContext(t *testing.T) {
	srv := newWebhookServer(t)
	reg, _ := loadSlack(t, map[string]any{
		"webhook_url": srv.URL,
		"changelog":   map[string]any{"enabled": true},
	})
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	w := cfg.Tasks[taskNotifySuccess].With
	for _, k := range []string{"changelog", "repo", "commit_hash", "previous_commit_hash"} {
		if s, _ := w[k].(string); s == "" {
			t.Errorf("success task with.%s not injected", k)
		}
	}
	if _, ok := cfg.Tasks[taskNotifyFail].With["changelog"]; ok {
		t.Error("fail task got changelog inputs, want success only")
	}
}

func TestConfigure_ChangelogValidation(t *testing.T) {
	_, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: map[string]any{
		"webhook_url": "https://example.com/hook",
		"changelog":   map[string]any{"enabled": true, "max_commits": -1},
	}}})
	if err == nil || !strings.Contains(err.Error(), "max_commits") {
		t.Fatalf("Load error = %v, want max_commits validation", err)
	}
	_, err = whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: map[string]any{
		"webhook_url": "https://example.com/hook",
		"changelog":   map[string]any{"authors": map[string]any{"a@example.com": " "}},
	}}})
	if err == nil || !strings.Contains(err.Error(), "empty Slack member ID") {
		t.Fatalf("Load error = %v, want authors validation", err)
	}
}
