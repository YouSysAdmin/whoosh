package aws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

const credsYAML = `aws_access_key_id: AKIAEXAMPLE
aws_secret_access_key: secretkey
aws_session_token: token123
aws_default_region: eu-west-1
`

func TestResolveCredentials_StaticVars(t *testing.T) {
	c := awsConfig{AccessKeyID: "AKIA", SecretAccessKey: "shh", SessionToken: "tok"}
	p, region, err := c.resolveCredentials(context.Background())
	if err != nil {
		t.Fatalf("resolveCredentials: %v", err)
	}
	if p == nil {
		t.Fatal("want a credentials provider for static vars")
	}
	if region != "" {
		t.Errorf("static vars region = %q, want empty (region comes from region:)", region)
	}
	got, err := p.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got.AccessKeyID != "AKIA" || got.SecretAccessKey != "shh" || got.SessionToken != "tok" {
		t.Errorf("provider returned %+v", got)
	}
}

func TestResolveCredentials_StaticVarsIncomplete(t *testing.T) {
	c := awsConfig{AccessKeyID: "AKIA"} // missing secret
	if _, _, err := c.resolveCredentials(context.Background()); err == nil {
		t.Fatal("expected error when only one static key is set")
	}
}

func TestResolveCredentials_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.yml")
	if err := os.WriteFile(path, []byte(credsYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	c := awsConfig{CredentialsFile: path}
	p, region, err := c.resolveCredentials(context.Background())
	if err != nil {
		t.Fatalf("resolveCredentials: %v", err)
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", region)
	}
	got, err := p.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got.AccessKeyID != "AKIAEXAMPLE" || got.SecretAccessKey != "secretkey" || got.SessionToken != "token123" {
		t.Errorf("provider returned %+v", got)
	}
}

func TestResolveCredentials_FileMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.yml")
	if err := os.WriteFile(path, []byte("aws_default_region: us-east-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := awsConfig{CredentialsFile: path}
	if _, _, err := c.resolveCredentials(context.Background()); err == nil {
		t.Fatal("expected error for credentials file missing access/secret keys")
	}
}

func TestResolveCredentials_URL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(credsYAML))
	}))
	defer srv.Close()

	c := awsConfig{CredentialsURL: srv.URL, CredentialsToken: "ghtoken"}
	p, region, err := c.resolveCredentials(context.Background())
	if err != nil {
		t.Fatalf("resolveCredentials: %v", err)
	}
	if gotAuth != "token ghtoken" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "token ghtoken")
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", region)
	}
	got, err := p.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got.AccessKeyID != "AKIAEXAMPLE" {
		t.Errorf("provider returned %+v", got)
	}
}

func TestResolveCredentials_URLNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := awsConfig{CredentialsURL: srv.URL}
	if _, _, err := c.resolveCredentials(context.Background()); err == nil {
		t.Fatal("expected error for non-200 credentials url response")
	}
}

// fakeIMDS answers the IMDSv2 curl sequence with canned values.
func fakeIMDS(ctx context.Context, cmd string) (string, error) {
	switch {
	case strings.Contains(cmd, "/api/token"):
		return "TOKEN123", nil
	case strings.Contains(cmd, "/placement/region"):
		return "eu-west-1", nil
	case strings.HasSuffix(cmd, "/security-credentials/"):
		return "my-role", nil
	case strings.Contains(cmd, "/security-credentials/my-role"):
		return `{"AccessKeyId":"AKIA","SecretAccessKey":"shh","Token":"sess"}`, nil
	}
	return "", fmt.Errorf("unexpected command: %s", cmd)
}

func TestFetchIMDS(t *testing.T) {
	got, err := fetchIMDS(context.Background(), fakeIMDS)
	if err != nil {
		t.Fatalf("fetchIMDS: %v", err)
	}
	want := imdsCreds{AccessKeyID: "AKIA", SecretAccessKey: "shh", SessionToken: "sess", Region: "eu-west-1"}
	if got != want {
		t.Errorf("fetchIMDS = %+v, want %+v", got, want)
	}
}

func TestFetchIMDS_EmptyToken(t *testing.T) {
	run := func(ctx context.Context, cmd string) (string, error) { return "", nil }
	if _, err := fetchIMDS(context.Background(), run); err == nil {
		t.Fatal("expected error when IMDS token is empty")
	}
}

func TestFetchIMDS_IncompleteCreds(t *testing.T) {
	run := func(ctx context.Context, cmd string) (string, error) {
		switch {
		case strings.Contains(cmd, "/api/token"):
			return "TOKEN123", nil
		case strings.Contains(cmd, "/placement/region"):
			return "eu-west-1", nil
		case strings.HasSuffix(cmd, "/security-credentials/"):
			return "my-role", nil
		default:
			return `{"AccessKeyId":"","SecretAccessKey":""}`, nil
		}
	}
	if _, err := fetchIMDS(context.Background(), run); err == nil {
		t.Fatal("expected error for incomplete credentials JSON")
	}
}

func TestResolveCredentials_FromHostRequiresHost(t *testing.T) {
	c := awsConfig{CredentialsFromHost: &credentialsHost{}}
	if _, _, err := c.resolveCredentials(context.Background()); err == nil {
		t.Fatal("expected error when credentials_from_host.host is empty")
	}
}

func TestSSHRunner_CapturesStdout(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile,
	}, ssh.Options{StrictHostKey: false})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got, err := sshRunner(conn)(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("sshRunner: %v", err)
	}
	if got != "hello" {
		t.Errorf("sshRunner stdout = %q, want %q", got, "hello")
	}
}

func TestResolveCredentials_DefaultChain(t *testing.T) {
	// No explicit source: fall through to the SDK default chain (nil provider).
	c := awsConfig{Region: "eu-west-1"}
	p, region, err := c.resolveCredentials(context.Background())
	if err != nil {
		t.Fatalf("resolveCredentials: %v", err)
	}
	if p != nil {
		t.Errorf("want nil provider (default chain), got %T", p)
	}
	if region != "" {
		t.Errorf("region from resolveCredentials = %q, want empty (region applied separately)", region)
	}
}
