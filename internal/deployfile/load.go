package deployfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// Load reads the shared Deployfile at deployfilePath, merges the per-stage file (deploy/<stage>.yml) on top, applies
// defaults, and validates the result.
// Each file may pull in shared fragments via `include:` (see resolveIncludes), those are merged underneath the file
// naming them before the base/stage merge.
func Load(deployfilePath, stage string) (*ast.DeployFile, error) {
	base, err := resolveIncludes(deployfilePath, nil, map[string]bool{})
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(deployfilePath)
	sp, err := stagePath(dir, stage)
	if err != nil {
		return nil, err
	}
	stageCfg, err := resolveIncludes(sp, nil, map[string]bool{})
	if err != nil {
		return nil, err
	}

	cfg := ast.Merge(base, stageCfg)
	cfg.Stage = stage
	cfg.Dir = filepath.Dir(deployfilePath)
	cfg.ApplyDefaults()

	if err := loadEnvFiles(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadEnvFiles reads each EnvFiles entry (relative to c.Dir, unless absolute) into c.EnvFileValues.
// Files are layered in listed order with later entries overriding earlier ones, a missing file is skipped, and a
// malformed file is an error.
func loadEnvFiles(c *ast.DeployFile) error {
	if len(c.EnvFiles) == 0 {
		return nil
	}
	values := map[string]string{}
	for _, p := range c.EnvFiles {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.Dir, p)
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			continue
		}
		kv, err := godotenv.Read(p)
		if err != nil {
			return fmt.Errorf("read env file %s: %w", p, err)
		}
		for k, v := range kv {
			values[k] = v
		}
	}
	c.EnvFileValues = values
	return nil
}

// readConfig parses a single YAML file into a DeployFile, rejecting unknown fields so typos surface early.
func readConfig(path string) (*ast.DeployFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c ast.DeployFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// resolveIncludes reads the file at path and merges any files it names via `include:` underneath it.
// Include paths resolve relative to path's directory (require_relative style), they are merged in listed order with the
// declaring file winning, and each include is itself resolved recursively. chain holds the absolute ancestor paths so a
// circular include is reported instead of looping. seen holds every file already merged into this resolution (fresh
// per root file), so a diamond - the same fragment reachable via two parents - is merged once instead of duplicating
// its concatenated fields (hosts, plugins, env_files, custom_phases); the second occurrence resolves to nil and is
// skipped.
func resolveIncludes(path string, chain []string, seen map[string]bool) (*ast.DeployFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for _, p := range chain {
		if p == abs {
			return nil, fmt.Errorf("circular include: %s (via %v)", abs, chain)
		}
	}
	if seen[abs] {
		return nil, nil
	}
	seen[abs] = true

	self, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	if len(self.Include) == 0 {
		return self, nil
	}

	chain = append(chain, abs)
	dir := filepath.Dir(path)
	merged := &ast.DeployFile{}
	for _, inc := range self.Include {
		p := inc
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		incCfg, err := resolveIncludes(p, chain, seen)
		if err != nil {
			return nil, err
		}
		if incCfg == nil { // already merged via an earlier include (diamond)
			continue
		}
		merged = ast.Merge(merged, incCfg)
	}
	merged = ast.Merge(merged, self) // the declaring file wins over its includes
	merged.Include = nil             // resolved; don't leak into `config`/{{.config}}
	return merged, nil
}

// ScriptsLocation returns the absolute directory that script names resolve against: scripts_dir if set (relative to the
// config-file dir), else scripts/ under whichever StageDirs entry the project uses, falling back to the preferred name
// when none exists yet.
func ScriptsLocation(c *ast.DeployFile) string {
	if c.ScriptsDir != "" {
		if filepath.IsAbs(c.ScriptsDir) {
			return c.ScriptsDir
		}
		return filepath.Join(c.Dir, c.ScriptsDir)
	}
	for _, sd := range StageDirs {
		if fi, err := os.Stat(filepath.Join(c.Dir, sd)); err == nil && fi.IsDir() {
			return filepath.Join(c.Dir, sd, "scripts")
		}
	}
	return filepath.Join(c.Dir, StageDirs[0], "scripts")
}
