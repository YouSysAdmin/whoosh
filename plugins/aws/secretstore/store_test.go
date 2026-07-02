package secretstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
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

// fakeSecrets serves ListSecrets from sequential pages per name-prefix filter (to exercise pagination) and
// GetSecretValue from a name->value map (unknown = ResourceNotFound).
type fakeSecrets struct {
	values    map[string]string     // secret name -> SecretString
	listPages map[string][][]string // filter prefix -> sequential pages of names
	listCalls map[string]int        // ListSecrets calls per filter prefix
}

func (f *fakeSecrets) ListSecrets(_ context.Context, in *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	if f.listCalls == nil {
		f.listCalls = map[string]int{}
	}
	var prefix string
	if len(in.Filters) > 0 && len(in.Filters[0].Values) > 0 {
		prefix = in.Filters[0].Values[0]
	}
	idx := f.listCalls[prefix]
	f.listCalls[prefix]++
	pages := f.listPages[prefix]
	if idx >= len(pages) {
		return &secretsmanager.ListSecretsOutput{}, nil
	}
	out := &secretsmanager.ListSecretsOutput{}
	for _, name := range pages[idx] {
		out.SecretList = append(out.SecretList, smtypes.SecretListEntry{Name: awssdk.String(name)})
	}
	if idx+1 < len(pages) {
		out.NextToken = awssdk.String(fmt.Sprintf("tok-%d", idx+1))
	}
	return out, nil
}

func (f *fakeSecrets) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	name := awssdk.ToString(in.SecretId)
	v, ok := f.values[name]
	if !ok {
		return nil, &smtypes.ResourceNotFoundException{}
	}
	return &secretsmanager.GetSecretValueOutput{Name: awssdk.String(name), SecretString: awssdk.String(v)}, nil
}

func TestSecretsEnvironmentFile(t *testing.T) {
	fake := &fakeSecrets{
		values: map[string]string{
			// A JSON-object secret expands into one var per key.
			"myapp/prod/app": `{"DATABASE_URL":"postgres://u:p@h/db","API_KEY":"abc"}`,
			// A plain-string secret -> one var keyed by the name's last segment.
			"myapp/prod/redis-url":   "redis://h:6379",
			"shared/github-auth-key": "ghp_abc123",
		},
		listPages: map[string][][]string{
			// Two pages -> exercises NextToken pagination.
			"myapp/prod/": {
				{"myapp/prod/app"},
				{"myapp/prod/redis-url"},
			},
		},
	}
	path := filepath.Join(t.TempDir(), ".env.local")
	s := &secretsPlugin{api: fake}

	with := map[string]any{
		"prefixes": []any{"myapp/prod/", "shared/github-auth-key"}, // trailing slash = set, none = single secret
		"path":     path,
	}
	if err := s.runEnvironmentFile(context.Background(), with, &bytes.Buffer{}); err != nil {
		t.Fatalf("runEnvironmentFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `API_KEY="abc"` + "\n" +
		`DATABASE_URL="postgres://u:p@h/db"` + "\n" +
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

	// myapp/prod/ listed twice (paged), the no-slash prefix never hit ListSecrets.
	if fake.listCalls["myapp/prod/"] != 2 {
		t.Errorf("expected 2 ListSecrets calls for myapp/prod/, got %d", fake.listCalls["myapp/prod/"])
	}
	if fake.listCalls["shared/github-auth-key"] != 0 {
		t.Errorf("a no-slash prefix must not call ListSecrets, got %d", fake.listCalls["shared/github-auth-key"])
	}
}

func TestSecretsEnvironmentFile_JSONOverride(t *testing.T) {
	// json:false keeps a JSON value as a single var keyed by the secret name.
	fake := &fakeSecrets{values: map[string]string{"myapp/prod/config": `{"A":"b"}`}}
	s := &secretsPlugin{api: fake}
	path := filepath.Join(t.TempDir(), ".env")
	with := map[string]any{"prefixes": []any{"myapp/prod/config"}, "path": path, "json": false}
	if err := s.runEnvironmentFile(context.Background(), with, &bytes.Buffer{}); err != nil {
		t.Fatalf("runEnvironmentFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if want := `CONFIG="{\"A\":\"b\"}"` + "\n"; string(got) != want {
		t.Fatalf("json:false:\n got: %q\nwant: %q", got, want)
	}

	// json:true on a non-object value is an error.
	fake2 := &fakeSecrets{values: map[string]string{"myapp/prod/plain": "just-a-string"}}
	s2 := &secretsPlugin{api: fake2}
	with2 := map[string]any{"prefixes": []any{"myapp/prod/plain"}, "path": filepath.Join(t.TempDir(), ".env"), "json": true}
	if err := s2.runEnvironmentFile(context.Background(), with2, &bytes.Buffer{}); err == nil {
		t.Error("json:true on a non-object secret should error")
	}
}

func TestSecretsEnvironmentFile_Validation(t *testing.T) {
	s := &secretsPlugin{api: &fakeSecrets{}}
	if err := s.runEnvironmentFile(context.Background(), map[string]any{"path": "x"}, &bytes.Buffer{}); err == nil {
		t.Error("missing prefixes should error")
	}
	if err := s.runEnvironmentFile(context.Background(), map[string]any{"prefixes": []any{"/a"}}, &bytes.Buffer{}); err == nil {
		t.Error("missing path should error")
	}
}

// When the executor supplies a HostFileWriter, the env-file action renders onto the hosts (not the operator machine),
// passing the path and dotenv content.
func TestSecretsEnvironmentFile_RendersOnHosts(t *testing.T) {
	fake := &fakeSecrets{
		values:    map[string]string{"myapp/prod/app": `{"DB":"x","TOKEN":"y"}`},
		listPages: map[string][][]string{"myapp/prod/": {{"myapp/prod/app"}}},
	}
	s := &secretsPlugin{api: fake}
	hw := &fakeHostWriter{}
	ctx := whoosh.WithHostFileWriter(context.Background(), hw)

	with := map[string]any{"prefixes": []any{"myapp/prod/"}, "path": "config/app.env"}
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

// The aws:secrets startup hook fetches once and injects secrets into the context: a JSON-object secret expands by key,
// a plain secret keys off its last name segment, and every value is registered for redaction.
func TestSecretsStartup_LoadsContext(t *testing.T) {
	fake := &fakeSecrets{
		values: map[string]string{
			"myapp/prod/app":   `{"DB_URL":"postgres://h/db","SECRET":"topsecretval"}`,
			"myapp/prod/token": "plain-token-value",
		},
		listPages: map[string][][]string{"myapp/prod/": {{"myapp/prod/app", "myapp/prod/token"}}},
	}
	s := &secretsPlugin{api: fake}
	cfg := &whoosh.DeployFile{}

	if err := s.startup(secretsContextParams{Prefixes: []string{"myapp/prod/"}})(context.Background(), cfg); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if got := cfg.Imports["secrets"]["DB_URL"]; got != "postgres://h/db" {
		t.Errorf(`Imports["secrets"]["DB_URL"] = %q, want "postgres://h/db"`, got)
	}
	if got := cfg.Imports["secrets"]["SECRET"]; got != "topsecretval" {
		t.Errorf(`Imports["secrets"]["SECRET"] = %q, want "topsecretval"`, got)
	}
	// A non-JSON secret is keyed by its last name segment (like the SSM startup).
	if got := cfg.Imports["secrets"]["token"]; got != "plain-token-value" {
		t.Errorf(`Imports["secrets"]["token"] = %q, want "plain-token-value"`, got)
	}
	// The value was registered for masking, so it's masked in any output.
	if red := whoosh.Masking("x=topsecretval"); strings.Contains(red, "topsecretval") {
		t.Errorf("secret value not registered for redaction: %q", red)
	}
}
