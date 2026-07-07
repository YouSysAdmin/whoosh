// Package operator resolves the identity of the person (or CI job) running whoosh, used for the deploy lock info,
// the revisions log, and the {{.deployer}} / $DEPLOYER template context.
package operator

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	once sync.Once
	name string
)

// Name returns the deploy operator identity, resolved once per process:
// DEPLOYER env var, then git config user.name, then USER env var, else "unknown".
func Name() string {
	once.Do(func() { name = resolve(gitUserName) })
	return name
}

// resolve implements the precedence with an injectable git lookup so tests do not depend on the machine's gitconfig.
func resolve(gitLookup func() string) string {
	if v := strings.TrimSpace(os.Getenv("DEPLOYER")); v != "" {
		return v
	}
	if v := strings.TrimSpace(gitLookup()); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	return "unknown"
}

// gitUserName reads git config user.name, returning "" when git is missing, hangs, or has no name configured.
func gitUserName() string {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gitPath, "config", "--get", "user.name").Output()
	if err != nil {
		return ""
	}
	return string(out)
}
