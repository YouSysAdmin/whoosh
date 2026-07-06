package varstmpl_test

import (
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

func TestRender_VarsAndContext(t *testing.T) {
	c := varstmpl.Context{
		Vars:        map[string]any{"RAILS_ENV": "production"},
		AppName:     "myapp",
		ReleasePath: "/var/www/myapp/releases/20260101000000",
		Host:        "web1",
	}
	got, err := varstmpl.Render("cd {{.release_path}} && RAILS_ENV={{.RAILS_ENV}} restart {{.app_name}} on {{.host}}", c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "cd /var/www/myapp/releases/20260101000000 && RAILS_ENV=production restart myapp on web1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_CommitHash(t *testing.T) {
	c := varstmpl.Context{CommitHash: "abc123"}
	got, err := varstmpl.Render("rev={{.commit_hash}}", c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "rev=abc123" {
		t.Fatalf("got %q, want rev=abc123", got)
	}
	// Unset commit_hash is an empty string, not a missing key (no error).
	if got, err := varstmpl.Render("rev={{.commit_hash}}", varstmpl.Context{}); err != nil || got != "rev=" {
		t.Fatalf("empty commit_hash render = %q, %v", got, err)
	}
}

func TestRender_UndefinedKeyIsError(t *testing.T) {
	if _, err := varstmpl.Render("hi {{.nope}}", varstmpl.Context{}); err == nil {
		t.Fatal("expected error for undefined key, got nil")
	}
}

func TestRender_TaskState(t *testing.T) {
	c := varstmpl.Context{Tasks: map[string]any{
		"whoami":    map[string]any{"Account": "123"},
		"fetch-cfg": map[string]any{"region": "eu-west-1"},
	}}
	// Dot access for identifier names; index for names with dashes.
	got, err := varstmpl.Render(`a={{ .tasks.whoami.Account }} r={{ index .tasks "fetch-cfg" "region" }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "a=123 r=eu-west-1" {
		t.Fatalf("got %q, want a=123 r=eu-west-1", got)
	}
}

func TestRenderWith_LenientMissingKey(t *testing.T) {
	// Strict (default) errors on a missing captured field; lenient renders it empty.
	c := varstmpl.Context{Tasks: map[string]any{"whoami": map[string]any{}}}
	if _, err := varstmpl.Render("{{ .tasks.whoami.Account }}", c); err == nil {
		t.Fatal("strict render should error on a missing field")
	}
	got, err := varstmpl.RenderWith("{{ .tasks.whoami.Account }}", c, false)
	if err != nil {
		t.Fatalf("lenient render: %v", err)
	}
	if got != "<no value>" {
		t.Fatalf("lenient render = %q, want <no value>", got)
	}
}

func TestRender_SprigHelpers(t *testing.T) {
	got, err := varstmpl.Render("{{.app_name | upper}}", varstmpl.Context{AppName: "myapp"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "MYAPP") {
		t.Fatalf("sprig upper not applied, got %q", got)
	}
}

// The full sprig set is documented as available - pin the serialization/list helpers the docs call out.
func TestRender_SprigSerializationHelpers(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{
		"opts": map[string]any{"region": "eu-west-1", "keep": 3},
		"svcs": []any{"web", "worker"},
	}}
	got, err := varstmpl.Render(`{{ toJson .opts }} {{ join "," .svcs }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := `{"keep":3,"region":"eu-west-1"} web,worker`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_ToYaml(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{
		"opts": map[string]any{"region": "eu-west-1", "tags": []any{"a", "b"}},
	}}
	got, err := varstmpl.Render("{{ toYaml .opts }}", c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// yaml.Marshal sorts map keys; the trailing newline is trimmed.
	want := "region: eu-west-1\ntags:\n    - a\n    - b"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_FromYaml(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{"y": "region: eu-west-1\nkeep: 3"}}
	got, err := varstmpl.Render("{{ (fromYaml .y).region }}/{{ (fromYaml .y).keep }}", c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "eu-west-1/3" {
		t.Fatalf("got %q, want eu-west-1/3", got)
	}
	// Invalid YAML fails the render.
	if _, err := varstmpl.Render("{{ fromYaml .y }}", varstmpl.Context{Vars: map[string]any{"y": ": ["}}); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestRender_FromYamlArray(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{"y": "- web1\n- web2"}}
	got, err := varstmpl.Render(`{{ join "," (fromYamlArray .y) }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "web1,web2" {
		t.Fatalf("got %q, want web1,web2", got)
	}
}

func TestRender_Required(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{"bucket": "my-bucket", "empty": ""}}
	got, err := varstmpl.Render(`{{ required "bucket must be set" .bucket }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "my-bucket" {
		t.Fatalf("got %q, want my-bucket", got)
	}
	// An empty value fails with the given message.
	_, err = varstmpl.Render(`{{ required "bucket must be set" .empty }}`, c)
	if err == nil || !strings.Contains(err.Error(), "bucket must be set") {
		t.Fatalf("expected error containing the message, got %v", err)
	}
}

func TestRender_BuiltinWinsOverVar(t *testing.T) {
	// A user var must not shadow a well-known key.
	c := varstmpl.Context{Vars: map[string]any{"app_name": "shadow"}, AppName: "real"}
	got, err := varstmpl.Render("{{.app_name}}", c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "real" {
		t.Fatalf("builtin should win, got %q", got)
	}
}

func TestRenderParams_DeepTemplating(t *testing.T) {
	c := varstmpl.Context{
		Vars:  map[string]any{"asg": "my-asg", "bastion": "10.0.0.9"},
		Stage: "prod",
	}
	in := map[string]any{
		"name":  "{{ .asg }}",                             // string from vars
		"stage": "{{ .stage }}",                           // static context
		"creds": map[string]any{"host": "{{ .bastion }}"}, // nested map
		"tags":  []any{"{{ .asg }}-a", "literal"},         // list
		"keep":  3,                                        // int preserved
		"flag":  true,                                     // bool preserved
	}
	out, err := varstmpl.RenderParams(in, c, true)
	if err != nil {
		t.Fatalf("RenderParams: %v", err)
	}
	if out["name"] != "my-asg" || out["stage"] != "prod" {
		t.Errorf("string render wrong: %v / %v", out["name"], out["stage"])
	}
	if m, _ := out["creds"].(map[string]any); m == nil || m["host"] != "10.0.0.9" {
		t.Errorf("nested map render wrong: %v", out["creds"])
	}
	if l, _ := out["tags"].([]any); len(l) != 2 || l[0] != "my-asg-a" || l[1] != "literal" {
		t.Errorf("list render wrong: %v", out["tags"])
	}
	if out["keep"] != 3 || out["flag"] != true {
		t.Errorf("non-strings changed: keep=%v flag=%v", out["keep"], out["flag"])
	}

	// Strict mode surfaces an undefined var (typo guard).
	if _, err := varstmpl.RenderParams(map[string]any{"x": "{{ .nope }}"}, c, true); err == nil {
		t.Error("expected strict render to error on an undefined var")
	}
}

// envSecret returns the env value for use in the command AND registers it with redact, so the same value is masked
// anywhere whoosh later prints it.
func TestEnvSecret_RegistersForRedaction(t *testing.T) {
	const val = "tok_live_not-a-known-pattern_998877"
	t.Setenv("WHOOSH_TEST_SECRET", val)

	got, err := varstmpl.Render(`auth {{ envSecret "WHOOSH_TEST_SECRET" }}`, varstmpl.Context{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The rendered command carries the real value (it must reach the server).
	if got != "auth "+val {
		t.Fatalf("envSecret should return the value, got %q", got)
	}
	// But the value is now registered, so redacting the command masks it.
	if red := masking.String(got); strings.Contains(red, val) {
		t.Errorf("envSecret value not redacted: %q", red)
	}
}

// env resolves from the process environment first, falling back to the context's env_files values.
func TestRender_EnvFallsBackToEnvFileValues(t *testing.T) {
	const name = "WHOOSH_TEST_ENVFILE_VAR"
	c := varstmpl.Context{EnvFileValues: map[string]string{name: "from-file"}}

	// Unset in the process env: the env_files value is used.
	got, err := varstmpl.Render(`v={{ env "`+name+`" }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "v=from-file" {
		t.Fatalf("env fallback = %q, want v=from-file", got)
	}

	// A set process var wins over the file value...
	t.Setenv(name, "from-proc")
	if got, _ := varstmpl.Render(`v={{ env "`+name+`" }}`, c); got != "v=from-proc" {
		t.Fatalf("process env should win, got %q", got)
	}
	// ...even when set to empty (dotenv non-override convention).
	t.Setenv(name, "")
	if got, _ := varstmpl.Render(`v={{ env "`+name+`" }}`, c); got != "v=" {
		t.Fatalf("set-but-empty process env should win, got %q", got)
	}

	// No env_files at all keeps sprig parity: unset renders empty.
	if got, _ := varstmpl.Render(`v={{ env "WHOOSH_TEST_DEFINITELY_UNSET" }}`, varstmpl.Context{}); got != "v=" {
		t.Fatalf("unset env without env_files = %q, want v=", got)
	}
}

// env consults the resolved global envs between the process environment and env_files.
func TestLookupEnv_GlobalEnvLayer(t *testing.T) {
	const name = "WHOOSH_TEST_GLOBALENV_VAR"
	c := varstmpl.Context{
		GlobalEnvValues: map[string]string{name: "from-global"},
		EnvFileValues:   map[string]string{name: "from-file"},
	}

	// Unset in the process env: the global env value wins over env_files.
	if got, _ := varstmpl.Render(`v={{ env "`+name+`" }}`, c); got != "v=from-global" {
		t.Fatalf("global env should win over env_files, got %q", got)
	}

	// A set-but-empty global entry still wins over env_files (dotenv convention).
	c.GlobalEnvValues = map[string]string{name: ""}
	if got, _ := varstmpl.Render(`v={{ env "`+name+`" }}`, c); got != "v=" {
		t.Fatalf("set-but-empty global env should win over env_files, got %q", got)
	}

	// envSecret on a global value registers it for redaction.
	const secret = "whoosh_test_global_secret_9876"
	c.GlobalEnvValues = map[string]string{name: secret}
	got, err := varstmpl.Render(`auth {{ envSecret "`+name+`" }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "auth "+secret {
		t.Fatalf("envSecret should return the global value, got %q", got)
	}
	if red := masking.String(got); strings.Contains(red, secret) {
		t.Errorf("envSecret global value not redacted: %q", red)
	}

	// A set process var wins over the global env.
	c.GlobalEnvValues = map[string]string{name: "from-global"}
	t.Setenv(name, "from-proc")
	if got, _ := varstmpl.Render(`v={{ env "`+name+`" }}`, c); got != "v=from-proc" {
		t.Fatalf("process env should win over global env, got %q", got)
	}
}

// envSecret resolves through the same env_files fallback and still registers the value for redaction.
func TestEnvSecret_EnvFileValueRedacted(t *testing.T) {
	const val = "filetok_not-a-known-pattern_112233"
	c := varstmpl.Context{EnvFileValues: map[string]string{"WHOOSH_TEST_FILE_SECRET": val}}
	got, err := varstmpl.Render(`auth {{ envSecret "WHOOSH_TEST_FILE_SECRET" }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "auth "+val {
		t.Fatalf("envSecret should return the env_files value, got %q", got)
	}
	if red := masking.String(got); strings.Contains(red, val) {
		t.Errorf("envSecret env_files value not redacted: %q", red)
	}
}

// sensitive marks an arbitrary value (var, expression) sensitive.
func TestSensitive_RegistersForRedaction(t *testing.T) {
	c := varstmpl.Context{Vars: map[string]any{"db_password": "pw_abc_XYZ_123456"}}
	got, err := varstmpl.Render(`PGPASSWORD={{ sensitive .db_password }}`, c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "PGPASSWORD=pw_abc_XYZ_123456" {
		t.Fatalf("sensitive should return the value, got %q", got)
	}
	if red := masking.String(got); strings.Contains(red, "pw_abc_XYZ_123456") {
		t.Errorf("sensitive value not redacted: %q", red)
	}
}
