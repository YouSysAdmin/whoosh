package ast

// Built-in deploy phase names. They double as hook points (before/after) for user tasks and plugins, in the order
// below. They live in the config model (not internal/deploy) so the public plugin SDK (the root whoosh package) can
// re-export them - a plugin hooking a phase shouldn't hardcode the string.
// The marker phases (init/started/updated/published/finished) carry no built-in command - they are stable hook anchors
// marking a meaningful moment, so a hook can target "provision the host" (init), "the release is built" (updated), or
// "the release is live" (published) without depending on an internal step name like symlink/publishing.
const (
	PhaseStarting   = "deploy:starting"   // acquire the deployment lock
	PhaseCheck      = "deploy:check"      // ensure the directory tree exists
	PhaseInit       = "deploy:init"       // marker: provision the host (install software / deps)
	PhaseStarted    = "deploy:started"    // marker: deploy underway, checks passed
	PhaseUpdating   = "deploy:updating"   // fetch the repo, build the new release
	PhaseSymlink    = "deploy:symlink"    // link shared files/dirs into the release
	PhaseUpdated    = "deploy:updated"    // marker: new release built & linked, not yet live
	PhasePublishing = "deploy:publishing" // swap the current symlink
	PhasePublished  = "deploy:published"  // marker: new release is live
	PhaseFinishing  = "deploy:finishing"  // log the revision, prune old releases
	PhaseFinished   = "deploy:finished"   // marker: deploy complete

	// PhaseFailed is not a lifecycle step: it is the hook key whose `after` tasks run when a deploy fails (e.g. to send a
	// failure notification). Those tasks see {{.error}} / $DEPLOY_ERROR.
	PhaseFailed = "deploy:failed"

	// PhaseRollback is the hook point for `whoosh <stage> deploy:rollback`.
	// It's before/after hooks wrap the current-symlink swap, `after` tasks run with current already pointing at the
	// restored (previous) release, so a task can fix up shared state - e.g. restore the asset manifest into a shared
	// public/assets after a rollback.
	PhaseRollback = "deploy:rollback"
)
