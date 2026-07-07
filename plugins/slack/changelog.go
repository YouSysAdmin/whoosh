package slack

import (
	"regexp"
	"strings"
)

const (
	// maxAttachmentsPerMessage is Slack's per-message attachment limit.
	maxAttachmentsPerMessage = 20
	// defaultMaxCommits bounds the displayed changelog when max_commits is unset; maxCommitsCap is the hard ceiling
	// (also whoosh core's capture cap).
	defaultMaxCommits = 20
	maxCommitsCap     = 100
	// changelogColor is the bar color of each commit attachment.
	changelogColor = "#36a64f"
)

// changelogParams is the params: changelog: block - post the commits between the previously deployed revision and the
// new one as attachments on the success notification.
type changelogParams struct {
	Enabled bool `yaml:"enabled"`
	// CommitURL builds each commit's link: a template with a "{hash}" token, or a prefix the SHA is appended to.
	// Empty derives https://<host>/<org>/<repo>/commit/ from the app repo remote. Deliberately not a Go template -
	// the load-time params render would consume it.
	CommitURL string `yaml:"commit_url"`
	// Authors maps a commit author email (lowercased) to a Slack member ID (U.../W...), mentioned on the commit.
	Authors map[string]string `yaml:"authors"`
	// MaxCommits bounds the log (default 20, capped at 100).
	MaxCommits int `yaml:"max_commits"`
}

// commit is one parsed changelog line (whoosh core's {{.changelog}} format: <sha>|<author>|<email>|<subject>).
type commit struct {
	SHA, Author, Email, Subject string
}

// commitSHARe matches an abbreviated-to-full git SHA - guards against malformed changelog lines.
var commitSHARe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func isCommitSHA(s string) bool { return commitSHARe.MatchString(s) }

// parseCommits parses the core {{.changelog}} value (newest first), skipping malformed lines and truncating to max.
// The subject may itself contain '|'.
func parseCommits(out string, max int) []commit {
	var cs []commit
	for _, line := range strings.Split(out, "\n") {
		if len(cs) == max {
			break
		}
		parts := strings.SplitN(strings.TrimSpace(line), "|", 4)
		if len(parts) != 4 || !isCommitSHA(parts[0]) {
			continue
		}
		cs = append(cs, commit{SHA: parts[0], Author: parts[1], Email: parts[2], Subject: parts[3]})
	}
	return cs
}

// deriveCommitURL turns a git remote into an https commit-URL prefix (trailing slash included), or "" when the remote
// is not URL-shaped (a local path, say) - then the commits simply carry no links.
func deriveCommitURL(repo string) string {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	var hostPath string
	switch {
	case strings.HasPrefix(repo, "https://") || strings.HasPrefix(repo, "http://"):
		hostPath = strings.SplitN(repo, "://", 2)[1]
	case strings.HasPrefix(repo, "ssh://"):
		hostPath = strings.TrimPrefix(repo, "ssh://")
		if at := strings.Index(hostPath, "@"); at >= 0 {
			hostPath = hostPath[at+1:]
		}
		// Drop an ssh port (host:22/org/repo).
		if slash := strings.Index(hostPath, "/"); slash > 0 {
			if colon := strings.Index(hostPath[:slash], ":"); colon >= 0 {
				hostPath = hostPath[:colon] + hostPath[slash:]
			}
		}
	case strings.Contains(repo, "@") && strings.Contains(repo, ":") && !strings.Contains(repo, "://"):
		// scp-like: git@host:org/repo
		rest := repo[strings.Index(repo, "@")+1:]
		hostPath = strings.Replace(rest, ":", "/", 1)
	default:
		return ""
	}
	hostPath = strings.Trim(hostPath, "/")
	// Need at least host/org/repo to build a commit link.
	if strings.Count(hostPath, "/") < 2 {
		return ""
	}
	return "https://" + hostPath + "/commit/"
}

// commitLink builds a commit's URL from the configured pattern/prefix, or "" when there is no base.
func commitLink(base, sha string) string {
	switch {
	case base == "":
		return ""
	case strings.Contains(base, "{hash}"):
		return strings.ReplaceAll(base, "{hash}", sha)
	default:
		return base + sha
	}
}

// changelogAttachments renders each commit as an attachment: author, subject as the (linked) title, and - when the
// author email maps to a Slack ID - a mention in the mrkdwn text (Slack does not render mrkdwn inside titles).
func changelogAttachments(commits []commit, urlBase string, authors map[string]string) []attachment {
	atts := make([]attachment, 0, len(commits))
	for _, c := range commits {
		a := attachment{
			Color:      changelogColor,
			AuthorName: c.Author,
			Title:      c.Subject,
			TitleLink:  commitLink(urlBase, c.SHA),
			Fallback:   c.Author + " - " + c.Subject + " - " + c.SHA,
		}
		if id := authors[strings.ToLower(c.Email)]; id != "" {
			a.Text = "<@" + id + ">"
			a.MrkdwnIn = []string{"text"}
		}
		atts = append(atts, a)
	}
	return atts
}

// batchAttachments splits the summary plus the commit attachments into per-message batches of at most size: the
// summary heads the first message, continuations carry commits only.
func batchAttachments(head attachment, rest []attachment, size int) [][]attachment {
	first := min(len(rest), size-1)
	batches := [][]attachment{append([]attachment{head}, rest[:first]...)}
	for rest = rest[first:]; len(rest) > 0; {
		n := min(len(rest), size)
		batches = append(batches, rest[:n])
		rest = rest[n:]
	}
	return batches
}

// changelogMax bounds the displayed commits: the configured max_commits, defaulted and capped.
func (n *notifier) changelogMax() int {
	m := n.cfg.Changelog.MaxCommits
	if m <= 0 {
		return defaultMaxCommits
	}
	return min(m, maxCommitsCap)
}

// commitURLBase resolves the commit-link base: the commit_url param when set, else derived from the repo remote.
func (n *notifier) commitURLBase(repo string) string {
	if cu := n.cfg.Changelog.CommitURL; cu != "" {
		return cu
	}
	return deriveCommitURL(repo)
}
