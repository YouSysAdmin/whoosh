package slack

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

const defaultGithubAPI = "https://api.github.com"

// displayDeployer resolves the name shown for the deployer: with deployer_github_lookup on and a login-shaped value
// (no spaces - a value with spaces is already a human name), the GitHub display name, else the value as-is. The
// lookup runs once per process (cached), is bounded by the notifier's HTTP timeout, and every failure - network,
// non-200, empty name - silently falls back to the login.
func (n *notifier) displayDeployer(ctx context.Context, login string) string {
	if !n.cfg.DeployerGithubLookup || login == "" || strings.ContainsAny(login, " \t") {
		return login
	}
	n.ghOnce.Do(func() {
		n.ghName = n.githubUserName(ctx, login)
	})
	if n.ghName == "" {
		return login
	}
	return n.ghName
}

// githubUserName fetches the user's display name from the GitHub users API, returning "" on any failure.
func (n *notifier) githubUserName(ctx context.Context, login string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.githubAPI+"/users/"+url.PathEscape(login), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := n.client.Do(req)
	if err != nil {
		slog.Debug("slack: github deployer lookup failed", "error", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Debug("slack: github deployer lookup failed", "status", resp.StatusCode)
		return ""
	}
	var user struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return ""
	}
	return strings.TrimSpace(user.Name)
}
