package ssm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/yousysadmin/whoosh"
)

// fakeHostWriter captures what an action asks to render onto the hosts.
type fakeHostWriter struct {
	path    string
	content []byte
}

func (f *fakeHostWriter) WriteFile(_ context.Context, path string, content []byte) error {
	f.path, f.content = path, content
	return nil
}

// fakeSSM serves GetParametersByPath from sequential pages per path (to exercise pagination) and GetParameter from a
// leaf map (the leaf-path fallback).
type fakeSSM struct {
	pages map[string][][]ssmtypes.Parameter
	leaf  map[string]string
	calls map[string]int
}

func (f *fakeSSM) GetParametersByPath(_ context.Context, in *awsssm.GetParametersByPathInput, _ ...func(*awsssm.Options)) (*awsssm.GetParametersByPathOutput, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	path := awssdk.ToString(in.Path)
	idx := f.calls[path]
	f.calls[path]++
	pages := f.pages[path]
	if idx >= len(pages) {
		return &awsssm.GetParametersByPathOutput{}, nil
	}
	out := &awsssm.GetParametersByPathOutput{Parameters: pages[idx]}
	if idx+1 < len(pages) {
		out.NextToken = awssdk.String(fmt.Sprintf("tok-%d", idx+1))
	}
	return out, nil
}

func (f *fakeSSM) GetParameter(_ context.Context, in *awsssm.GetParameterInput, _ ...func(*awsssm.Options)) (*awsssm.GetParameterOutput, error) {
	name := awssdk.ToString(in.Name)
	v, ok := f.leaf[name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &awsssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: awssdk.String(name), Value: awssdk.String(v)}}, nil
}

func ssmParam(name, value string) ssmtypes.Parameter {
	return ssmtypes.Parameter{Name: awssdk.String(name), Value: awssdk.String(value)}
}

func TestSSMEnvironmentFile(t *testing.T) {
	fake := &fakeSSM{
		pages: map[string][][]ssmtypes.Parameter{
			// Queried as Path="/app/prod" (the trailing slash is trimmed). Two pages -> exercises NextToken pagination.
			"/app/prod": {
				{ssmParam("/app/prod/DATABASE_URL", "postgres://u:p@h/db")},
				{ssmParam("/app/prod/redis-url", "redis://h:6379"), ssmParam("/app/prod/EMPTY", "")},
			},
		},
		// No trailing slash -> a single parameter, fetched via GetParameter.
		leaf: map[string]string{"/shared/github-auth-key": "ghp_abc123"},
	}
	path := filepath.Join(t.TempDir(), ".env.local")
	s := &ssmPlugin{api: fake}

	var buf bytes.Buffer
	with := map[string]any{
		"prefixes": []any{"/app/prod/", "/shared/github-auth-key"}, // trailing slash = tree; none = single param
		"path":     path,
	}
	if err := s.runEnvironmentFile(context.Background(), with, &buf); err != nil {
		t.Fatalf("runEnvironmentFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `DATABASE_URL="postgres://u:p@h/db"` + "\n" +
		"EMPTY=\n" +
		`GITHUB_AUTH_KEY="ghp_abc123"` + "\n" +
		`REDIS_URL="redis://h:6379"` + "\n"
	if string(got) != want {
		t.Fatalf("env file mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}

	// Secrets on disk -> 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("env file mode = %v, want 0600", perm)
	}

	// /app/prod/ paged twice; the no-slash prefix never hit GetParametersByPath.
	if fake.calls["/app/prod"] != 2 {
		t.Errorf("expected 2 GetParametersByPath calls for /app/prod, got %d", fake.calls["/app/prod"])
	}
	if fake.calls["/shared/github-auth-key"] != 0 {
		t.Errorf("a no-slash prefix must not call GetParametersByPath, got %d", fake.calls["/shared/github-auth-key"])
	}
}

func TestSSMEnvironmentFile_Validation(t *testing.T) {
	s := &ssmPlugin{api: &fakeSSM{}}
	if err := s.runEnvironmentFile(context.Background(), map[string]any{"path": "x"}, &bytes.Buffer{}); err == nil {
		t.Error("missing prefixes should error")
	}
	if err := s.runEnvironmentFile(context.Background(), map[string]any{"prefixes": []any{"/a"}}, &bytes.Buffer{}); err == nil {
		t.Error("missing path should error")
	}
}

// The aws:ssm startup hook fetches once and injects parameters into the context namespace (keyed by last path segment),
// registering each value for redaction.
func TestSSMStartup_LoadsContext(t *testing.T) {
	fake := &fakeSSM{
		pages: map[string][][]ssmtypes.Parameter{
			"/app/prod": {{ssmParam("/app/prod/secret", "topsecretval"), ssmParam("/app/prod/DB_URL", "postgres://h/db")}},
		},
	}
	s := &ssmPlugin{api: fake}
	cfg := &whoosh.DeployFile{}

	if err := s.startup(ssmContextParams{Prefixes: []string{"/app/prod/"}})(context.Background(), cfg); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if got := cfg.Imports["ssm"]["secret"]; got != "topsecretval" {
		t.Errorf(`Imports["ssm"]["secret"] = %q, want "topsecretval"`, got)
	}
	if got := cfg.Imports["ssm"]["DB_URL"]; got != "postgres://h/db" {
		t.Errorf(`Imports["ssm"]["DB_URL"] = %q, want "postgres://h/db"`, got)
	}
	// The value was registered for masking, so it's masked in any output.
	if red := whoosh.Masking("token=topsecretval"); strings.Contains(red, "topsecretval") {
		t.Errorf("SSM value not registered for redaction: %q", red)
	}
}

// When the executor supplies a HostFileWriter, the env-file action renders onto the hosts (not the operator machine),
// passing the path and dotenv content.
func TestSSMEnvironmentFile_RendersOnHosts(t *testing.T) {
	fake := &fakeSSM{pages: map[string][][]ssmtypes.Parameter{
		"/app/prod": {{ssmParam("/app/prod/DB", "x"), ssmParam("/app/prod/TOKEN", "y")}},
	}}
	s := &ssmPlugin{api: fake}
	hw := &fakeHostWriter{}
	ctx := whoosh.WithHostFileWriter(context.Background(), hw)

	with := map[string]any{"prefixes": []any{"/app/prod/"}, "path": "config/app.env"}
	if err := s.runEnvironmentFile(ctx, with, &bytes.Buffer{}); err != nil {
		t.Fatalf("runEnvironmentFile: %v", err)
	}
	if hw.path != "config/app.env" {
		t.Errorf("host path = %q, want config/app.env", hw.path)
	}
	want := `DB="x"` + "\n" + `TOKEN="y"` + "\n"
	if string(hw.content) != want {
		t.Errorf("host content = %q, want %q", hw.content, want)
	}
}
