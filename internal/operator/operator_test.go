package operator

import "testing"

func TestResolve_Precedence(t *testing.T) {
	git := func() string { return "Git Name\n" }
	noGit := func() string { return "" }

	tests := []struct {
		name           string
		deployer, user string
		gitLookup      func() string
		want           string
	}{
		{"deployer env wins", "Ops Bot", "alice", git, "Ops Bot"},
		{"git name when deployer unset", "", "alice", git, "Git Name"},
		{"user fallback without git", "", "alice", noGit, "alice"},
		{"unknown when everything empty", "", "", noGit, "unknown"},
		{"blank deployer falls through", "   ", "alice", noGit, "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEPLOYER", tt.deployer)
			t.Setenv("USER", tt.user)
			if got := resolve(tt.gitLookup); got != tt.want {
				t.Fatalf("resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}
