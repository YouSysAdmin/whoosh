// Package deployfile loads and merges the Deployfile configuration that drives a deployment: a shared Deployfile.yml
// plus a per-stage deploy/<stage>.yml.
package deployfile

import (
	"fmt"
	"os"
	"path/filepath"
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
