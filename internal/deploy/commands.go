package deploy

import (
	"path"
	"regexp"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/paths"
	"github.com/yousysadmin/whoosh/internal/shtmpl"
)

// ensureStructureCmd creates the base tree plus the shared parents of every linked dir/file, and verifies each linked
// file already exists in shared/.
// The dir creation is idempotent, linked dirs are app-writable so they're created, but linked files are
// operator-provided config (database.yml, .env, ...) - a missing one fails deploy:check with a clear message rather
// than becoming a dangling symlink at publish time.
func ensureStructureCmd(l paths.Layout, linkedFiles, linkedDirs []string) string {
	dirs := []string{l.ReleasesPath, l.SharedPath}
	for _, d := range linkedDirs {
		// the shared-side source is what must exist, the rewrite only affects the release side
		src, _ := ast.ParseLinkedPath(d)
		dirs = append(dirs, path.Join(l.SharedPath, src))
	}
	var files []string
	for _, f := range linkedFiles {
		src, _ := ast.ParseLinkedPath(f)
		shared := path.Join(l.SharedPath, src)
		dirs = append(dirs, path.Dir(shared))
		files = append(files, shared)
	}
	return shtmpl.MustRender(tmpl, "ensure_structure.sh.tmpl", struct {
		Dirs  []string
		Files []string
	}{dirs, files})
}

// linkSpec is one shared->release symlink, with the parent dirs that must exist.
type linkSpec struct {
	Shared       string
	Target       string
	SharedParent string
	TargetParent string
}

// symlinkSharedScript links each linked dir/file from shared/ into the release, removing whatever the checkout placed
// there first.
func symlinkSharedScript(l paths.Layout, release string, linkedFiles, linkedDirs []string) string {
	var dirs, files []linkSpec
	for _, d := range linkedDirs {
		src, dst := ast.ParseLinkedPath(d) // "source:dest" links shared/source -> release/dest
		shared, target := path.Join(l.SharedPath, src), path.Join(release, dst)
		dirs = append(dirs, linkSpec{Shared: shared, Target: target, TargetParent: path.Dir(target)})
	}
	for _, f := range linkedFiles {
		src, dst := ast.ParseLinkedPath(f)
		shared, target := path.Join(l.SharedPath, src), path.Join(release, dst)
		files = append(files, linkSpec{
			Shared: shared, Target: target,
			SharedParent: path.Dir(shared), TargetParent: path.Dir(target),
		})
	}
	return shtmpl.MustRender(tmpl, "symlink_shared.sh.tmpl", struct {
		Dirs  []linkSpec
		Files []linkSpec
	}{dirs, files})
}

// publishCmd atomically repoints current at release.
// The mv -T form is an atomic rename on GNU coreutils (Linux targets), the fallback covers BSD mv (e.g. macOS) which
// lacks -T, at the cost of a microscopic non-atomic window.
func publishCmd(release, current string) string {
	return shtmpl.MustRender(tmpl, "publish.sh.tmpl", struct{ Release, Current, Tmp string }{
		Release: release, Current: current, Tmp: current + ".tmp",
	})
}

// writeRevisionCmd records the deployed commit and time inside the release: <release>/REVISION (the git SHA from the
// mirror) and <release>/REVISION_TIME.
func writeRevisionCmd(repoPath, release, branch, revTime string) string {
	return shtmpl.MustRender(tmpl, "write_revision.sh.tmpl", struct {
		Repo, Branch, RevisionFile, TimeFile, Time string
	}{
		Repo:         repoPath,
		Branch:       branch,
		RevisionFile: path.Join(release, "REVISION"),
		TimeFile:     path.Join(release, "REVISION_TIME"),
		Time:         revTime,
	})
}

// currentRevisionCmd reads <current>/REVISION - the SHA the live release was deployed from - tolerating absence
// (fresh deploy, or a release predating the REVISION file).
func currentRevisionCmd(l paths.Layout) string {
	return shtmpl.MustRender(tmpl, "current_revision.sh.tmpl", struct{ RevisionFile string }{
		RevisionFile: path.Join(l.CurrentPath, "REVISION"),
	})
}

// commitSHARe matches an abbreviated-to-full git SHA, guarding against a corrupt or unexpected REVISION file.
var commitSHARe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func isCommitSHA(s string) bool { return commitSHARe.MatchString(s) }

// changelogMaxCommits caps the captured changelog - consumers truncate further for display.
const changelogMaxCommits = 100

// changelogCmd lists the commits between the previously deployed revision and the new one from the repo mirror, one
// per line as <sha>|<author>|<email>|<subject> (the {{.changelog}} / $DEPLOY_CHANGELOG contract). Both SHAs must be
// pre-validated with isCommitSHA.
func changelogCmd(repoPath, prev, cur string) string {
	return shtmpl.MustRender(tmpl, "changelog.sh.tmpl", struct {
		Repo, Range string
		Max         int
	}{Repo: repoPath, Range: prev + ".." + cur, Max: changelogMaxCommits})
}

// logRevisionCmd appends a line to <deploy_to>/revisions.log, reading the SHA from the release's REVISION file written
// by writeRevisionCmd.
func logRevisionCmd(l paths.Layout, release, branch, releaseTimestamp, user string) string {
	return shtmpl.MustRender(tmpl, "log_revision.sh.tmpl", struct {
		Branch, RevisionFile, ReleaseTimestamp, User, LogFile string
	}{
		Branch:           branch,
		RevisionFile:     path.Join(release, "REVISION"),
		ReleaseTimestamp: releaseTimestamp,
		User:             user,
		LogFile:          path.Join(l.DeployTo, "revisions.log"),
	})
}

// cleanupCmd removes all but the newest `keep` releases.
// Releases are timestamp-named, so a lexical sort is chronological.
func cleanupCmd(releasesPath string, keep int) string {
	return shtmpl.MustRender(tmpl, "cleanup.sh.tmpl", struct {
		ReleasesPath string
		Keep         int
	}{ReleasesPath: releasesPath, Keep: keep})
}

// lockCmd creates the deploy lock file atomically (noclobber), failing if another deploy holds it. info records
// who/when, written into the file as it's acquired and shown when the lock is contended.
func lockCmd(l paths.Layout, info string) string {
	return shtmpl.MustRender(tmpl, "lock.sh.tmpl", struct{ DeployTo, Lock, Info string }{
		DeployTo: l.DeployTo, Lock: path.Join(l.DeployTo, ".deploy.lock"), Info: info,
	})
}

// unlockCmd removes the deploy lock.
func unlockCmd(l paths.Layout) string {
	return shtmpl.MustRender(tmpl, "unlock.sh.tmpl", struct{ Lock string }{
		Lock: path.Join(l.DeployTo, ".deploy.lock"),
	})
}

// rollbackScript repoints current at the release immediately older than the one it currently targets, atomically.
// When cleanup is set, the rolled-back release is removed.
// The deploy_to path is controlled config, so it is embedded in double quotes to allow the $prev/$curbase shell variables.
func rollbackScript(l paths.Layout, cleanup bool) string {
	return shtmpl.MustRender(tmpl, "rollback.sh.tmpl", struct {
		ReleasesPath string
		CurrentPath  string
		Cleanup      bool
	}{ReleasesPath: l.ReleasesPath, CurrentPath: l.CurrentPath, Cleanup: cleanup})
}

// releasesScript lists releases (oldest first), marking the current one.
func releasesScript(l paths.Layout) string {
	return shtmpl.MustRender(tmpl, "releases.sh.tmpl", struct{ CurrentPath, ReleasesPath string }{
		CurrentPath: l.CurrentPath, ReleasesPath: l.ReleasesPath,
	})
}
