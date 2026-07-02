// Package paths computes the on-target directory layout for a deployment.
// All paths are POSIX (remote hosts), so it uses path, not path/filepath.
package paths

import "path"

// Whoosh uses a directory structure similar to Capistrano.
// Because I think this structure is ideal :)

//  /var/www/hello-svc/
//  +-- current -> releases/20230624120000   # atomically swapped symlink
//  +-- releases/
//  |   +-- 20230624120000/                  # timestamped checkout
//  |   +-- ...                              # last keep_releases kept
//  +-- repo/                                # git mirror cache
//  +-- shared/                              # .env, log/, tmp/ - linked into each release

// Layout is the release directory tree rooted at DeployTo.
type Layout struct {
	DeployTo     string // e.g. /var/www/myapp
	ReleasesPath string // <deploy_to>/releases
	SharedPath   string // <deploy_to>/shared
	RepoPath     string // <deploy_to>/repo (git mirror cache)
	CurrentPath  string // <deploy_to>/current (symlink to the live release)
}

// For returns the layout for a deploy_to root.
func For(deployTo string) Layout {
	return Layout{
		DeployTo:     deployTo,
		ReleasesPath: path.Join(deployTo, "releases"),
		SharedPath:   path.Join(deployTo, "shared"),
		RepoPath:     path.Join(deployTo, "repo"),
		CurrentPath:  path.Join(deployTo, "current"),
	}
}

// Release returns the path to a specific timestamped release.
func (l Layout) Release(timestamp string) string {
	return path.Join(l.ReleasesPath, timestamp)
}
