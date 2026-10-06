package whoosh_test

import (
	"testing"

	"github.com/yousysadmin/whoosh"
)

// Writing into the merged params never edits the inputs, maps inside slices included.
func TestMergeParams_NoAliasing(t *testing.T) {
	base := map[string]any{
		"tags":  []any{map[string]any{"k": "base"}},
		"inner": map[string]any{"a": 1},
	}
	out := whoosh.MergeParams(base, map[string]any{"inner": map[string]any{"b": 2}})

	out["tags"].([]any)[0].(map[string]any)["k"] = "changed"
	out["inner"].(map[string]any)["a"] = 9

	if got := base["tags"].([]any)[0].(map[string]any)["k"]; got != "base" {
		t.Errorf("base slice element changed to %v", got)
	}

	if got := base["inner"].(map[string]any)["a"]; got != 1 {
		t.Errorf("base nested map changed to %v", got)
	}

	if got := out["inner"].(map[string]any)["b"]; got != 2 {
		t.Errorf("merged b = %v, want 2", got)
	}
}
