package deployfile

import (
	"path/filepath"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// TasksForStage returns the task map of the shared Deployfile merged with deploy/<stage>.yml, so tasks defined only in
// a stage file are discoverable (and thus invocable as `whoosh <stage> <task>`) too.
// If the stage file is absent or unreadable it falls back to the shared file's tasks alone - registration is
// best-effort and must not fail on an unknown stage.
// It returns the full *Task (not just a summary) so callers see fields like Desc and Hidden without a second load.
func TasksForStage(path, stage string) (map[string]*ast.Task, error) {
	// One seen set spans both resolutions, mirroring Load: when the Deployfile itself includes the stage file, the
	// stage resolution is a no-op instead of re-merging it on top with the opposite precedence - discovery must show
	// the same task definitions the actual run uses.
	seen := map[string]bool{}
	base, err := resolveIncludes(path, nil, seen)
	if err != nil {
		return nil, err
	}
	cfg := base
	if sp, err := stagePath(filepath.Dir(path), stage); err == nil {
		if stageCfg, err := resolveIncludes(sp, nil, seen); err == nil && stageCfg != nil {
			cfg = ast.Merge(base, stageCfg)
		}
	}
	return cfg.Tasks, nil
}
