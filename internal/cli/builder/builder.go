// Package builder implements the `whoosh build` subcommand: it composes a custom whoosh binary that bundles the plugins
// you choose - including your own private or third-party plugin modules.
//
//	whoosh build \
//	  --with github.com/acme/whoosh-datadog \
//	  --with github.com/acme/private-plugin@v1.2.0 \
//	  --replace github.com/acme/private-plugin=../local \
//	  -o ./whoosh
//
// Private modules use your normal Go auth (GOPRIVATE + ~/.netrc or SSH insteadOf).
// Use --replace points a module at a local checkout.
package builder

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// whooshModule is the import path of the public whoosh package the generated main.go calls into, and the module that
// gets pinned/replaced.
const whooshModule = "github.com/yousysadmin/whoosh"

// buildOptions are the resolved flags for `whoosh build`.
type buildOptions struct {
	withs         []string // plugin modules, each "module[@version]"
	replaces      []string // go.mod replace specs, each "old[@v]=new[@v]"
	output        string
	whooshVersion string
	appVersion    string
	tags          string
	goBin         string
	keep          bool
	verbose       bool
}

// modVer is a module path with an optional version ("" means resolve to latest).
type modVer struct {
	path    string
	version string
}

func parseModVer(s string) (modVer, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return modVer{}, fmt.Errorf("empty module")
	}
	if i := strings.Index(s, "@"); i >= 0 {
		path, ver := s[:i], s[i+1:]
		if path == "" || ver == "" {
			return modVer{}, fmt.Errorf("invalid module@version %q", s)
		}
		return modVer{path: path, version: ver}, nil
	}
	return modVer{path: s}, nil
}

// replaceOldPath returns the module path on the left side of a replacement spec (stripping any version), e.g.
// "github.com/x" from "github.com/x@v1=./fork".
func replaceOldPath(spec string) string {
	left := spec
	if eq := strings.Index(spec, "="); eq >= 0 {
		left = spec[:eq]
	}
	left = strings.TrimSpace(left)
	if i := strings.Index(left, "@"); i >= 0 {
		left = left[:i]
	}
	return left
}

// replaceIsFilesystem reports whether a replacement points at a local directory (Go's rule: the target begins with ./
// or ../ or is absolute).
// Those modules are resolved by the replacement + `go mod tidy`, so skip `go get` for them (a local/unpublished module
// has no version to fetch).
func replaceIsFilesystem(spec string) bool {
	eq := strings.Index(spec, "=")
	if eq < 0 {
		return false
	}
	rhs := strings.TrimSpace(spec[eq+1:])
	return strings.HasPrefix(rhs, "./") || strings.HasPrefix(rhs, "../") ||
		strings.HasPrefix(rhs, ".\\") || strings.HasPrefix(rhs, "..\\") ||
		filepath.IsAbs(rhs)
}

// runBuild composes a throwaway module in a temp dir that imports whoosh plus the requested plugins, then `go build's
// it.
func runBuild(opts buildOptions) error {
	output := opts.output
	if output == "" {
		output = "whoosh"
	}
	// Resolve the output path now (relative to the caller cwd): the build runs from a temp dir, where a relative -o would
	// land in the wrong place.
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	plugins := make([]modVer, 0, len(opts.withs))
	for _, w := range opts.withs {
		mv, err := parseModVer(w)
		if err != nil {
			return fmt.Errorf("--with %q: %w", w, err)
		}
		plugins = append(plugins, mv)
	}

	// Modules replaced to a local dir are resolved without a network lookup.
	fsReplaced := map[string]bool{}
	for _, r := range opts.replaces {
		if replaceIsFilesystem(r) {
			fsReplaced[replaceOldPath(r)] = true
		}
	}

	dir, err := os.MkdirTemp("", "whoosh-build-*")
	if err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	if opts.keep {
		slog.Info("keeping build dir", "dir", dir)
	} else {
		defer os.RemoveAll(dir)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(generateMain(plugins)), 0o644); err != nil {
		return fmt.Errorf("write main.go: %w", err)
	}

	goBin := opts.goBin
	if goBin == "" {
		goBin = "go"
	}
	run := func(args ...string) error { return runGo(goBin, dir, opts.verbose, args...) }

	if err := run("mod", "init", "whoosh.custom"); err != nil {
		return err
	}
	// Replaces first, so version resolution below honors local/forked modules.
	for _, r := range opts.replaces {
		if err := run("mod", "edit", "-replace="+r); err != nil {
			return err
		}
	}
	// Pin whoosh itself unless it's served from a local replace.
	if !fsReplaced[whooshModule] {
		if err := run("get", whooshModule+"@"+versionOrLatest(opts.whooshVersion)); err != nil {
			return err
		}
	}
	// Pin each plugin module unless locally replaced.
	for _, p := range plugins {
		if fsReplaced[p.path] {
			continue
		}
		if err := run("get", p.path+"@"+versionOrLatest(p.version)); err != nil {
			return err
		}
	}
	// Resolve the remaining graph + write go.sum.
	if err := run("mod", "tidy"); err != nil {
		return err
	}

	buildArgs := []string{"build", "-trimpath", "-ldflags", ldflags(opts), "-o", absOutput}
	if opts.tags != "" {
		buildArgs = append(buildArgs, "-tags", opts.tags)
	}
	buildArgs = append(buildArgs, ".")
	if err := run(buildArgs...); err != nil {
		return err
	}
	slog.Info("built binary", "path", absOutput)
	return nil
}

func versionOrLatest(v string) string {
	if v == "" {
		return "latest"
	}
	return v
}

// ldflags mirrors the Makefile (-s -w + the version.Version stamp) so a custom build matches an official one.
// The version stamp is skipped for the unhelpful "latest" value, leaving Go's build-info version to surface instead.
func ldflags(opts buildOptions) string {
	flags := "-s -w"
	ver := opts.appVersion
	if ver == "" {
		ver = opts.whooshVersion
	}
	if ver != "" && ver != "latest" {
		flags += " -X " + whooshModule + "/internal/version.Version=" + ver
	}
	return flags
}

func runGo(goBin, dir string, verbose bool, args ...string) error {
	if verbose {
		slog.Info("exec", "cmd", goBin+" "+strings.Join(args, " "))
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ() // GOOS/GOARCH/GOPRIVATE/netrc/etc.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// generateMain renders the throwaway entrypoint: call entrypoint.Main(), blank-import the core plugins (always
// built in) and each requested plugin module.
func generateMain(plugins []modVer) string {
	var b strings.Builder
	b.WriteString("// Code generated by `whoosh build`. DO NOT EDIT.\n")
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"" + whooshModule + "/entrypoint\"\n")
	b.WriteString("\t_ \"" + whooshModule + "/plugins/core\"\n")
	for _, p := range plugins {
		b.WriteString("\t_ \"" + p.path + "\"\n")
	}
	b.WriteString(")\n\n")
	b.WriteString("func main() { entrypoint.Main() }\n")
	return b.String()
}
