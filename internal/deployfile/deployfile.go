// Package deployfile loads and merges the Deployfile configuration that drives a deployment: a shared Deployfile.yml
// plus a per-stage deploy/<stage>.yml.
package deployfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDeployfiles is the list of shared config-file names searched for, in order.
// Both the tool-specific Whooshfile and the generic Deployfile are accepted (first match wins), so a project may use
// either variation.
var DefaultDeployfiles = []string{
	"Whooshfile.yml",
	"Whooshfile.yaml",
	"whooshfile.yml",
	"whooshfile.yaml",
	"Whooshfile",
	"Deployfile.yml",
	"Deployfile.yaml",
	"deployfile.yml",
	"deployfile.yaml",
	"Deployfile",
	// dotted
	".Whooshfile.yml",
	".Whooshfile.yaml",
	".whooshfile.yml",
	".whooshfile.yaml",
	".Whooshfile",
	".Deployfile.yml",
	".Deployfile.yaml",
	".deployfile.yml",
	".deployfile.yaml",
	".Deployfile",
}

// StageDirs are the directory names (relative to the config file) that may hold the per-stage files, tried in order.
// Both the tool-specific "whoosh" and the generic "deploy" are accepted, so a project may use either variation.
var StageDirs = []string{"whoosh", ".whoosh", "deploy", ".deploy"}

// stageExts is the set of extensions tried when resolving <stagedir>/<stage>.<ext>.
var stageExts = []string{".yml", ".yaml"}

// Discover returns the path to the shared Deployfile.
// If override is non-empty it is returned as-is (after an existence check), otherwise dir is searched for the first
// matching name in DefaultDeployfiles.
func Discover(dir, override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("deployfile %q: %w", override, err)
		}
		return override, nil
	}
	for _, name := range DefaultDeployfiles {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Deployfile found in %s (looked for %v)", dir, DefaultDeployfiles)
}

// StageInfo describes one available stage: its name, the stage file's path relative to the Deployfile's directory,
// and the file's root `description:` (empty when the file has none or does not parse).
type StageInfo struct {
	Name        string
	Path        string
	Description string
}

// ListStages returns the stages available next to the Deployfile: every plain <stagedir>/<name>.<ext> file, with the
// same StageDirs/stageExts precedence stagePath uses for a duplicate name. Subdirectories (shared fragments, scripts)
// are ignored, missing stage dirs are skipped, and the result is sorted by name.
// The description comes from the raw stage file alone (includes are not resolved); a file that fails to parse is
// still listed, with an empty description, so one broken stage never hides the rest.
func ListStages(deployfileDir string) ([]StageInfo, error) {
	extRank := func(name string) int {
		for i, ext := range stageExts {
			if filepath.Ext(name) == ext {
				return i
			}
		}
		return -1
	}
	seen := map[string]bool{}
	var stages []StageInfo
	for _, sd := range StageDirs {
		entries, err := os.ReadDir(filepath.Join(deployfileDir, sd))
		if err != nil {
			continue
		}
		// Within one dir, walk the extensions in stagePath's order so <stage>.yml wins over <stage>.yaml.
		for _, rank := range []int{0, 1} {
			for _, e := range entries {
				if e.IsDir() || extRank(e.Name()) != rank {
					continue
				}
				name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				if seen[name] {
					continue
				}
				seen[name] = true
				rel := filepath.Join(sd, e.Name())
				info := StageInfo{Name: name, Path: rel}
				if cfg, err := readConfig(filepath.Join(deployfileDir, rel)); err == nil {
					info.Description = cfg.Description
				}
				stages = append(stages, info)
			}
		}
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i].Name < stages[j].Name })
	return stages, nil
}

// stagePath resolves <stagedir>/<stage>.<ext> relative to the directory containing the shared config file, trying each
// StageDirs entry and extension in order. It returns the first existing path.
func stagePath(deployfileDir, stage string) (string, error) {
	for _, sd := range StageDirs {
		for _, ext := range stageExts {
			p := filepath.Join(deployfileDir, sd, stage+ext)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("stage %q not found: expected %s/%s.yml (in one of %v)", stage, StageDirs[0], stage, StageDirs)
}
