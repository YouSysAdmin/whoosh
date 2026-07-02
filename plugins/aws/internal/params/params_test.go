package params

import "testing"

func TestMerge(t *testing.T) {
	// Task `with:` (over) wins per key; nested maps merge recursively; a nil base yields the over map unchanged.
	if got := Merge(nil, map[string]any{"a": 1}); got["a"] != 1 {
		t.Fatalf("nil base: got %v", got)
	}

	base := map[string]any{
		"name":        "base",
		"keep":        "frombase",
		"source_tags": map[string]any{"App": "x", "Env": "uat"},
	}
	over := map[string]any{
		"name":        "task",                           // scalar override
		"source_tags": map[string]any{"Role": "worker"}, // nested merge
	}
	got := Merge(base, over)

	if got["name"] != "task" {
		t.Errorf("scalar override: name = %v, want task", got["name"])
	}
	if got["keep"] != "frombase" {
		t.Errorf("default-only key dropped: keep = %v, want frombase", got["keep"])
	}
	st, ok := got["source_tags"].(map[string]any)
	if !ok || st["App"] != "x" || st["Env"] != "uat" || st["Role"] != "worker" {
		t.Errorf("nested merge: source_tags = %v, want {App:x, Env:uat, Role:worker}", got["source_tags"])
	}

	// Merge must not mutate its inputs.
	if base["name"] != "base" || len(base["source_tags"].(map[string]any)) != 2 {
		t.Errorf("base was mutated: %v", base)
	}
}

func TestDecodeFeature(t *testing.T) {
	type p struct {
		Name string `yaml:"name"`
		Keep string `yaml:"keep"`
	}
	var got p
	if err := DecodeFeature(
		map[string]any{"name": "base", "keep": "frombase"}, // feature defaults
		map[string]any{"name": "task"},                     // task with: (wins)
		&got,
	); err != nil {
		t.Fatalf("DecodeFeature: %v", err)
	}
	if got.Name != "task" || got.Keep != "frombase" {
		t.Fatalf("got %+v, want {Name:task Keep:frombase}", got)
	}
}
