package deployfile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListStages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deploy", "uat.yml"), "description: \"uat environment https://example.com\"\n")
	writeFile(t, filepath.Join(dir, "deploy", "prod.yaml"), "vars:\n  a: b\n")
	// Subdirectories (shared fragments, scripts) are not stages.
	writeFile(t, filepath.Join(dir, "deploy", "shared", "frag.yaml"), "tasks: {}\n")
	writeFile(t, filepath.Join(dir, "deploy", "scripts", "x.yml"), "not: a stage\n")
	// A malformed stage file still lists, just without a description.
	writeFile(t, filepath.Join(dir, "deploy", "broken.yml"), "hosts: {not a list\n")
	// The whoosh/ dir has higher precedence than deploy/ for a duplicate name.
	writeFile(t, filepath.Join(dir, "whoosh", "uat.yaml"), "description: from whoosh dir\n")

	stages, err := ListStages(dir)
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	want := []StageInfo{
		{Name: "broken", Path: filepath.Join("deploy", "broken.yml")},
		{Name: "prod", Path: filepath.Join("deploy", "prod.yaml")},
		{Name: "uat", Path: filepath.Join("whoosh", "uat.yaml"), Description: "from whoosh dir"},
	}
	if len(stages) != len(want) {
		t.Fatalf("got %d stages %v, want %d", len(stages), stages, len(want))
	}
	for i, w := range want {
		if stages[i] != w {
			t.Errorf("stages[%d] = %+v, want %+v", i, stages[i], w)
		}
	}
}

func TestListStages_ExtensionPrecedence(t *testing.T) {
	dir := t.TempDir()
	// Within one dir .yml wins over .yaml, mirroring stagePath.
	writeFile(t, filepath.Join(dir, "deploy", "uat.yaml"), "description: yaml file\n")
	writeFile(t, filepath.Join(dir, "deploy", "uat.yml"), "description: yml file\n")

	stages, err := ListStages(dir)
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	if len(stages) != 1 || stages[0].Description != "yml file" {
		t.Errorf("got %+v, want the single .yml stage", stages)
	}
}

func TestListStages_NoStageDir(t *testing.T) {
	stages, err := ListStages(t.TempDir())
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	if len(stages) != 0 {
		t.Errorf("got %+v, want none", stages)
	}
}
