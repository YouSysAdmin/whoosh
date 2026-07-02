// Package scm builds the shell commands that fetch source onto a target host.
// The git strategy keeps a mirror clone under <deploy_to>/repo and materializes a release by archiving a branch into
// the release directory (no .git in the release).
package scm

import "github.com/yousysadmin/whoosh/internal/shtmpl"

// Git is the git fetch strategy.
type Git struct {
	Repo   string // clone URL
	Branch string // branch or ref to deploy
}

// EnsureMirror returns a command that creates the mirror cache if missing or updates it otherwise.
func (g Git) EnsureMirror(repoPath string) string {
	return shtmpl.MustRender(tmpls, "ensure_mirror.sh.tmpl", struct{ Repo, URL string }{
		Repo: repoPath, URL: g.Repo,
	})
}

// CreateRelease returns a command that extracts the branch into releasePath.
func (g Git) CreateRelease(repoPath, releasePath string) string {
	return shtmpl.MustRender(tmpls, "create_release.sh.tmpl", struct{ Repo, Release, Branch string }{
		Repo: repoPath, Release: releasePath, Branch: g.Branch,
	})
}

// Revision returns a command that prints the commit SHA the branch points to in the mirror.
// The deployment lifecycle captures its output into the template context as {{.commit_hash}} / $COMMIT_HASH.
func (g Git) Revision(repoPath string) string {
	return shtmpl.MustRender(tmpls, "revision.sh.tmpl", struct{ Repo, Branch string }{
		Repo: repoPath, Branch: g.Branch,
	})
}
