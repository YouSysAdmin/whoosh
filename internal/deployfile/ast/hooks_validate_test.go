package ast

import (
	"strings"
	"testing"
)

func hooksConfig(before, after map[string][]string) *DeployFile {
	return &DeployFile{
		Tasks: map[string]*Task{"notify": {Cmds: []string{"true"}}, "migrate": {Cmds: []string{"true"}}},
		CustomPhases: []CustomPhase{
			{Name: "deploy:migrated", After: PhaseUpdated, Task: "migrate"},
		},
		Hooks: Hooks{Before: before, After: after},
	}
}

func TestValidateHooks(t *testing.T) {
	cases := []struct {
		name    string
		before  map[string][]string
		after   map[string][]string
		wantErr string // empty = valid
	}{
		{name: "built-in phase key", after: map[string][]string{PhaseFinished: {"notify"}}},
		{name: "failed and rollback keys", after: map[string][]string{PhaseFailed: {"notify"}, PhaseRollback: {"notify"}}},
		{name: "custom phase key", after: map[string][]string{"deploy:migrated": {"notify"}}},
		{name: "task name key", before: map[string][]string{"migrate": {"notify"}}},
		{
			name:    "typo'd phase key",
			after:   map[string][]string{"deploy:publish": {"notify"}},
			wantErr: "deploy:publish",
		},
		{
			name:    "before deploy:failed never fires",
			before:  map[string][]string{PhaseFailed: {"notify"}},
			wantErr: "never run",
		},
		{
			name:    "unknown hook task",
			after:   map[string][]string{PhaseFinished: {"nope"}},
			wantErr: `task "nope" not found`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := hooksConfig(c.before, c.after).ValidateHooks()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateHooks: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ValidateHooks = %v, want error containing %q", err, c.wantErr)
			}
		})
	}
}
