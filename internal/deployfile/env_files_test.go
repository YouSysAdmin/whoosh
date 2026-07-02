package deployfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/yousysadmin/whoosh/internal/deployfile"
)

func TestLoad_EnvFiles(t *testing.T) {
	shared := `app:
  name: a
  repo: git@example.com:a.git
  deploy_to: /srv/a
env_files:
  - base.env
  - missing.env
  - override.env
`
	path := writeProject(t, shared, "prod", "{}\n")
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, "base.env"), []byte("FOO=base\nBAR=base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "override.env"), []byte("BAR=override\nBAZ=secret-baz-value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := deployfile.Load(path, "prod")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Later files override earlier ones; a missing file is skipped silently.
	want := map[string]string{"FOO": "base", "BAR": "override", "BAZ": "secret-baz-value"}
	for k, v := range want {
		if cfg.EnvFileValues[k] != v {
			t.Errorf("EnvFileValues[%q] = %q, want %q", k, cfg.EnvFileValues[k], v)
		}
	}

	// The loaded values must never be serialized into the resolved config (so .env secrets do not appear in
	// `config`/{{.config}}); the paths may.
	blob, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "secret-baz-value") {
		t.Errorf("env-file value leaked into serialized config:\n%s", blob)
	}
	if !strings.Contains(string(blob), "base.env") {
		t.Errorf("env_files paths should appear in config; got:\n%s", blob)
	}
}

func TestLoad_LogMerge(t *testing.T) {
	shared := `app:
  name: a
  repo: git@example.com:a.git
  deploy_to: /srv/a
log:
  level: info
  format: text
`
	stage := `log:
  format: json
  file: deploy.log
`
	path := writeProject(t, shared, "prod", stage)
	cfg, err := deployfile.Load(path, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("log.level = %q, want info (from base)", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("log.format = %q, want json (stage overrides base)", cfg.Log.Format)
	}
	if cfg.Log.File != "deploy.log" {
		t.Errorf("log.file = %q, want deploy.log (from stage)", cfg.Log.File)
	}
}
