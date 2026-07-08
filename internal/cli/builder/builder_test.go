package builder

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModVer(t *testing.T) {
	cases := []struct {
		in      string
		path    string
		version string
		wantErr bool
	}{
		{in: "github.com/acme/x", path: "github.com/acme/x"},
		{in: "github.com/acme/x@v1.2.0", path: "github.com/acme/x", version: "v1.2.0"},
		{in: "  github.com/acme/x@latest  ", path: "github.com/acme/x", version: "latest"},
		{in: "", wantErr: true},
		{in: "github.com/acme/x@", wantErr: true},
		{in: "@v1", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseModVer(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseModVer(%q): want error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseModVer(%q): %v", c.in, err)
			continue
		}
		if got.path != c.path || got.version != c.version {
			t.Errorf("parseModVer(%q) = %+v, want {%q %q}", c.in, got, c.path, c.version)
		}
	}
}

func TestReplaceHelpers(t *testing.T) {
	cases := []struct {
		spec   string
		oldPth string
		isFS   bool
	}{
		{spec: "github.com/x=./local", oldPth: "github.com/x", isFS: true},
		{spec: "github.com/x=../local", oldPth: "github.com/x", isFS: true},
		{spec: "github.com/x@v1=/abs/path", oldPth: "github.com/x", isFS: true},
		{spec: "github.com/x=github.com/y@v1.2.0", oldPth: "github.com/x", isFS: false},
		{spec: "github.com/x", oldPth: "github.com/x", isFS: false},
	}
	for _, c := range cases {
		if got := replaceOldPath(c.spec); got != c.oldPth {
			t.Errorf("replaceOldPath(%q) = %q, want %q", c.spec, got, c.oldPth)
		}
		if got := replaceIsFilesystem(c.spec); got != c.isFS {
			t.Errorf("replaceIsFilesystem(%q) = %v, want %v", c.spec, got, c.isFS)
		}
	}
}

func TestGenerateMain(t *testing.T) {
	src := generateMain([]modVer{{path: "github.com/acme/x"}})
	for _, want := range []string{
		`"github.com/yousysadmin/whoosh/entrypoint"`,
		`_ "github.com/yousysadmin/whoosh/plugins/core"`,
		`_ "github.com/acme/x"`,
		`func main() { entrypoint.Main() }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generateMain missing %q in:\n%s", want, src)
		}
	}
}

// TestBuild_IncludesPlugin is the end-to-end check: build a custom binary that adds two --with modules from the local
// checkout (so no network) - the aws plugin module and the example out-of-tree plugin - then confirms the binary
// reports both. Shells out to `go build`, so it's skipped under -short.
func TestBuild_IncludesPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: shells out to `go build`")
	}
	// This test lives at internal/cli/builder, so the repo root is three levels up.
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	exampleDir := filepath.Join(repoRoot, "examples", "plugins/hello")
	awsDir := filepath.Join(repoRoot, "plugins", "aws")
	out := filepath.Join(t.TempDir(), "whoosh-custom")

	err = runBuild(buildOptions{
		withs: []string{
			"github.com/yousysadmin/whoosh/plugins/aws",
			"github.com/yousysadmin/whoosh-example-hello",
		},
		replaces: []string{
			"github.com/yousysadmin/whoosh=" + repoRoot,
			"github.com/yousysadmin/whoosh/plugins/aws=" + awsDir,
			"github.com/yousysadmin/whoosh-example-hello=" + exampleDir,
		},
		output:     out,
		appVersion: "test",
	})
	if err != nil {
		t.Fatalf("runBuild: %v", err)
	}

	got, err := exec.Command(out, "plugins").Output()
	if err != nil {
		t.Fatalf("run built binary: %v", err)
	}
	for _, want := range []string{"aws", "hello"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("built binary `plugins` = %q, want it to contain %q", got, want)
		}
	}
}
