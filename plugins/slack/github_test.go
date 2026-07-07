package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// githubStub serves the users API, counting requests.
func githubStub(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func lookupNotifier(gh *httptest.Server, enabled bool) *notifier {
	return &notifier{
		cfg:       params{DeployerGithubLookup: enabled},
		client:    &http.Client{Timeout: time.Second},
		now:       time.Now,
		githubAPI: gh.URL,
	}
}

func TestDisplayDeployer(t *testing.T) {
	t.Run("name from github", func(t *testing.T) {
		gh, calls := githubStub(t, http.StatusOK, `{"name": "Andrii K"}`)
		n := lookupNotifier(gh, true)
		if got := n.displayDeployer(context.Background(), "andriy2152"); got != "Andrii K" {
			t.Errorf("displayDeployer = %q, want the GitHub name", got)
		}
		// Cached: a second display does not re-dial.
		if got := n.displayDeployer(context.Background(), "andriy2152"); got != "Andrii K" {
			t.Errorf("second displayDeployer = %q", got)
		}
		if calls.Load() != 1 {
			t.Errorf("github calls = %d, want 1 (cached)", calls.Load())
		}
	})

	t.Run("empty name falls back", func(t *testing.T) {
		gh, _ := githubStub(t, http.StatusOK, `{"name": null}`)
		if got := lookupNotifier(gh, true).displayDeployer(context.Background(), "ghost"); got != "ghost" {
			t.Errorf("displayDeployer = %q, want the login", got)
		}
	})

	t.Run("404 falls back", func(t *testing.T) {
		gh, _ := githubStub(t, http.StatusNotFound, `{"message":"Not Found"}`)
		if got := lookupNotifier(gh, true).displayDeployer(context.Background(), "nobody"); got != "nobody" {
			t.Errorf("displayDeployer = %q, want the login", got)
		}
	})

	t.Run("network error falls back", func(t *testing.T) {
		gh, _ := githubStub(t, http.StatusOK, `{}`)
		gh.Close()
		if got := lookupNotifier(gh, true).displayDeployer(context.Background(), "nobody"); got != "nobody" {
			t.Errorf("displayDeployer = %q, want the login", got)
		}
	})

	t.Run("disabled never dials", func(t *testing.T) {
		gh, calls := githubStub(t, http.StatusOK, `{"name": "X"}`)
		if got := lookupNotifier(gh, false).displayDeployer(context.Background(), "login"); got != "login" {
			t.Errorf("displayDeployer = %q, want the login untouched", got)
		}
		if calls.Load() != 0 {
			t.Errorf("github calls = %d, want 0 when disabled", calls.Load())
		}
	})

	t.Run("full name skips the lookup", func(t *testing.T) {
		gh, calls := githubStub(t, http.StatusOK, `{"name": "X"}`)
		if got := lookupNotifier(gh, true).displayDeployer(context.Background(), "Jane Doe"); got != "Jane Doe" {
			t.Errorf("displayDeployer = %q, want the name untouched", got)
		}
		if calls.Load() != 0 {
			t.Errorf("github calls = %d, want 0 for a value with spaces", calls.Load())
		}
	})
}
