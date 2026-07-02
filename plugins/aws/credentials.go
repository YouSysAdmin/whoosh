package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"gopkg.in/yaml.v3"

	"github.com/yousysadmin/whoosh/transport/ssh"
)

// awsConfig holds the AWS connection and credential params shared by every AWS plugins (inlined into each plugin's params).
// Region/profile pin the SDK config, the credential fields select an explicit source:
//
//   - access_key_id / secret_access_key / session_token - static keys
//   - credentials_file - a local YAML file of aws_* keys
//   - credentials_url (+ credentials_token) - the same YAML fetched over HTTP with an "Authorization: token <token>" header
//   - credentials_from_host - connect to a remote host over SSH and read temporary credentials from its EC2 instance metadata
//
// When no explicit source is set, the SDK default chain is used: environment variables, the shared config/profile,
// and the local EC2 instance IAM role.
// Precedence among explicit sources: static keys, file, url, then credentials_from_host.
type awsConfig struct {
	Region  string `yaml:"region"`
	Profile string `yaml:"profile"`

	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	SessionToken    string `yaml:"session_token"`

	CredentialsFile  string `yaml:"credentials_file"`
	CredentialsURL   string `yaml:"credentials_url"`
	CredentialsToken string `yaml:"credentials_token"`

	// CredentialsFromHost reads temporary credentials from a remote host's EC2 instance metadata (IMDSv2) over SSH - for
	// when the operator has no AWS credentials but a reachable instance carries an IAM role.
	CredentialsFromHost *credentialsHost `yaml:"credentials_from_host"`
}

// credentialsHost is the SSH target whose instance metadata supplies credentials.
type credentialsHost struct {
	Host           string `yaml:"host"`
	User           string `yaml:"user"`
	Port           int    `yaml:"port"`
	IdentityFile   string `yaml:"identity_file"`
	StrictHostKey  *bool  `yaml:"strict_host_key"`
	KnownHostsFile string `yaml:"known_hosts_file"`
}

// loadAWS builds an aws.Config, resolving credentials from the configured source (or the SDK default chain) and
// applying the region.
func loadAWS(ctx context.Context, c awsConfig) (awssdk.Config, error) {
	creds, region, err := c.resolveCredentials(ctx)
	if err != nil {
		return awssdk.Config{}, err
	}

	var opts []func(*config.LoadOptions) error
	if region == "" {
		region = c.Region
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	switch {
	case creds != nil:
		opts = append(opts, config.WithCredentialsProvider(creds))
	case c.Profile != "":
		opts = append(opts, config.WithSharedConfigProfile(c.Profile))
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

// resolveCredentials returns an explicit credentials provider and region for the configured source.
// A nil provider means "use the SDK default chain" (env, shared config/profile, EC2 IAM role).
func (c awsConfig) resolveCredentials(ctx context.Context) (awssdk.CredentialsProvider, string, error) {
	switch {
	case c.AccessKeyID != "" || c.SecretAccessKey != "":
		if c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return nil, "", fmt.Errorf("aws: access_key_id and secret_access_key must both be set")
		}
		return credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken), "", nil
	case c.CredentialsFile != "":
		cf, err := readCredentialsFile(c.CredentialsFile)
		if err != nil {
			return nil, "", err
		}
		return cf.provider(), cf.region(), nil
	case c.CredentialsURL != "":
		cf, err := fetchCredentialsURL(ctx, c.CredentialsURL, c.CredentialsToken)
		if err != nil {
			return nil, "", err
		}
		return cf.provider(), cf.region(), nil
	case c.CredentialsFromHost != nil:
		return fetchCredentialsFromHost(ctx, *c.CredentialsFromHost)
	default:
		return nil, "", nil
	}
}

// credentialsFile is the YAML schema for a credentials file or URL, its keys are the lowercased AWS env var names.
type credentialsFile struct {
	AccessKeyID     string `yaml:"aws_access_key_id"`
	SecretAccessKey string `yaml:"aws_secret_access_key"`
	SessionToken    string `yaml:"aws_session_token"`
	DefaultRegion   string `yaml:"aws_default_region"`
	Region          string `yaml:"aws_region"`
}

func (f credentialsFile) provider() awssdk.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(f.AccessKeyID, f.SecretAccessKey, f.SessionToken)
}

func (f credentialsFile) region() string {
	if f.DefaultRegion != "" {
		return f.DefaultRegion
	}
	return f.Region
}

// readCredentialsFile loads and validates a local YAML credentials file.
func readCredentialsFile(path string) (credentialsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("aws: read credentials file %q: %w", path, err)
	}
	return parseCredentials(data, path)
}

// credentialsHTTPTimeout bounds the credentials_url fetch. Configure runs on context.Background() (the plugin
// interface carries no context), so without a client timeout a hung server would block startup indefinitely and
// un-interruptibly.
const credentialsHTTPTimeout = 30 * time.Second

var credentialsHTTPClient = &http.Client{Timeout: credentialsHTTPTimeout}

// fetchCredentialsURL retrieves the credentials YAML over HTTP.
// When token is set it is sent as "Authorization: token <token>" (GitHub raw/contents API).
func fetchCredentialsURL(ctx context.Context, url, token string) (credentialsFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("aws: credentials url: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := credentialsHTTPClient.Do(req)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("aws: fetch credentials url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return credentialsFile{}, fmt.Errorf("aws: credentials url returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return credentialsFile{}, fmt.Errorf("aws: read credentials url body: %w", err)
	}
	return parseCredentials(body, "url")
}

// parseCredentials unmarshals and validates the credentials YAML.
func parseCredentials(data []byte, source string) (credentialsFile, error) {
	var f credentialsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return credentialsFile{}, fmt.Errorf("aws: parse credentials %s: %w", source, err)
	}
	if f.AccessKeyID == "" || f.SecretAccessKey == "" {
		return credentialsFile{}, fmt.Errorf("aws: credentials %s missing aws_access_key_id/aws_secret_access_key", source)
	}
	return f, nil
}

// imdsCreds are the temporary credentials read from a host's instance metadata.
type imdsCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

// fetchCredentialsFromHost connects to the host over SSH and reads temporary credentials from its EC2 instance metadata
// (IMDSv2).
func fetchCredentialsFromHost(ctx context.Context, h credentialsHost) (awssdk.CredentialsProvider, string, error) {
	if h.Host == "" {
		return nil, "", fmt.Errorf("aws: credentials_from_host.host is required")
	}
	conn, err := ssh.Dial(ctx, ssh.Target{
		Host:         h.Host,
		Port:         h.Port,
		User:         h.User,
		IdentityFile: h.IdentityFile,
	}, ssh.Options{
		StrictHostKey:  h.StrictHostKey == nil || *h.StrictHostKey,
		KnownHostsFile: h.KnownHostsFile,
	})
	if err != nil {
		return nil, "", fmt.Errorf("aws: connect to credentials host %q: %w", h.Host, err)
	}
	defer conn.Close()

	creds, err := fetchIMDS(ctx, sshRunner(conn))
	if err != nil {
		return nil, "", fmt.Errorf("aws: credentials from host %q: %w", h.Host, err)
	}
	return credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken), creds.Region, nil
}

// commandRunner runs a shell command on some host and returns its trimmed stdout.
type commandRunner func(ctx context.Context, cmd string) (string, error)

// sshRunner adapts an SSH connection to a commandRunner, capturing stdout.
func sshRunner(conn *ssh.Client) commandRunner {
	return func(ctx context.Context, cmd string) (string, error) {
		var out, errb bytes.Buffer
		if err := conn.Run(ctx, cmd, &out, &errb); err != nil {
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return strings.TrimSpace(out.String()), nil
	}
}

const imdsBase = "http://169.254.169.254/latest"

// fetchIMDS walks the IMDSv2 token -> region -> role -> credentials sequence using run to execute each curl on the
// target host. The token is requested with a 6h TTL (21600s), far more than one credential fetch needs.
func fetchIMDS(ctx context.Context, run commandRunner) (imdsCreds, error) {
	token, err := runNonEmpty(ctx, run, "obtain IMDSv2 token",
		"curl -sS -X PUT '"+imdsBase+"/api/token' -H 'X-aws-ec2-metadata-token-ttl-seconds: 21600'")
	if err != nil {
		return imdsCreds{}, err
	}
	hdr := "-H 'X-aws-ec2-metadata-token: " + token + "'"

	region, err := runNonEmpty(ctx, run, "obtain region from instance metadata",
		"curl -sS "+hdr+" "+imdsBase+"/meta-data/placement/region")
	if err != nil {
		return imdsCreds{}, err
	}
	role, err := runNonEmpty(ctx, run, "obtain IAM role name from instance metadata",
		"curl -sS "+hdr+" "+imdsBase+"/meta-data/iam/security-credentials/")
	if err != nil {
		return imdsCreds{}, err
	}
	body, err := runNonEmpty(ctx, run, "obtain IAM role credentials from instance metadata",
		"curl -sS "+hdr+" "+imdsBase+"/meta-data/iam/security-credentials/"+role)
	if err != nil {
		return imdsCreds{}, err
	}

	var c struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return imdsCreds{}, fmt.Errorf("parse IAM role credentials: %w", err)
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return imdsCreds{}, fmt.Errorf("instance metadata returned incomplete credentials")
	}
	return imdsCreds{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.Token, Region: region}, nil
}

// runNonEmpty runs cmd and fails with a labeled error if it errors or is empty.
func runNonEmpty(ctx context.Context, run commandRunner, what, cmd string) (string, error) {
	v, err := run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if v == "" {
		return "", fmt.Errorf("%s: empty response", what)
	}
	return v, nil
}
