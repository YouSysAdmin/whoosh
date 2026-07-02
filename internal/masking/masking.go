// Package masking scrubs secrets from text before it reaches the console or logs.
// Command output (e.g. a gem source URL with an embedded token) regularly leaks credentials in plain text, String
// applies a set of well-known secret patterns and Writer wraps an io.Writer to masking streamed output line by line.
// In addition to the built-in patterns, callers can register exact secret values with AddSecret (driven by the
// `envSecret`/`sensitive` template helpers) so a value the patterns don't recognize is still masked everywhere it
// appears.
package masking

import (
	"bytes"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Placeholder replaces a matched secret.
const Placeholder = "[FILTERED]"

// enabled gates masking process-wide.
// It defaults on, the CLI turns it off at --log-level debug, where the operator has asked to see everything.
var enabled atomic.Bool

func init() { enabled.Store(true) }

// SetEnabled turns masking on or off for the whole process.
func SetEnabled(on bool) { enabled.Store(on) }

// Enabled reports whether filtration is currently applied.
func Enabled() bool { return enabled.Load() }

// minSecretLen guards against masking trivially short values (e.g. "1", "ab"), which would mangle unrelated output.
// Mark only real secrets sensitive.
const minSecretLen = 4

var (
	secretsMu sync.RWMutex
	secrets   []string            // registered literal secrets, longest first
	secretSet = map[string]bool{} // dedup
)

// AddSecret registers a literal value to masking from all output, regardless of whether it matches a built-in pattern -
// the user-side counterpart to rules, driven by the `envSecret`/`sensitive` template helpers.
// Values shorter than minSecretLen (after trimming) are ignored, so an empty or near-empty env var can't blank out the
// logs. Safe for concurrent use.
func AddSecret(s string) {
	s = strings.TrimSpace(s)
	if len(s) < minSecretLen {
		return
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	if secretSet[s] {
		return
	}
	secretSet[s] = true
	secrets = append(secrets, s)
	// Longest-first, so a secret that contains a shorter registered one is masked whole rather than leaving a tail behind.
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
}

// replaceSecrets masks every registered literal secret in s.
func replaceSecrets(s string) string {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, Placeholder)
		}
	}
	return s
}

type rule struct {
	re   *regexp.Regexp
	repl string
}

// rules are applied in order.
// Token-format patterns are case-sensitive (their prefixes are), keyword=value patterns are case-insensitive.
// Patterns with a capture group keep the non-secret prefix (key name, URL userinfo) and masking only the value.
var rules = []rule{
	// URL basic-auth: scheme://user:PASSWORD@host -> keep scheme/user, masking password.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^\s:/@]+:)[^\s/@]+(@)`), `${1}` + Placeholder + `${2}`},
	// AWS access key IDs.
	{regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA)[0-9A-Z]{16}\b`), Placeholder},
	// aws_secret_access_key = <40 chars> (keep the key name).
	{regexp.MustCompile(`(?i)(aws_secret_access_key\s*[:=]\s*)[A-Za-z0-9/+=]{40}`), `${1}` + Placeholder},
	// GitHub tokens: personal/oauth/user/server/refresh, and fine-grained PATs.
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}\b`), Placeholder},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{82}\b`), Placeholder},
	// Slack bot/user tokens and incoming-webhook URLs.
	{regexp.MustCompile(`\bxox[abprs]-[0-9]+-[0-9]+(?:-[0-9]+)?-[A-Za-z0-9-]{16,}\b`), Placeholder},
	{regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]{20,}`), Placeholder},
	// Stripe live secret/restricted keys.
	{regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{24,}\b`), Placeholder},
	// SendGrid API key.
	{regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b`), Placeholder},
	// Google API key.
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), Placeholder},
	// npm token.
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), Placeholder},
	// JWT (header.payload.signature, base64url).
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), Placeholder},
	// Cloudflare API key/token = <value> (keep the key name).
	{regexp.MustCompile(`(?i)((?:CLOUDFLARE|CF)_(?:API_KEY|API_TOKEN|GLOBAL_API_KEY)\s*[:=]\s*["']?)[A-Za-z0-9_-]{32,}`), `${1}` + Placeholder},
	// JumpCloud API key/token = <40 chars>.
	{regexp.MustCompile(`(?i)(JUMPCLOUD_(?:API_KEY|TOKEN)\s*[:=]\s*["']?)[A-Za-z0-9]{40}\b`), `${1}` + Placeholder},
	// Generic secret-ish key = value (conservative: 6+ non-delimiter chars).
	{regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|auth[_-]?token|access[_-]?token)["']?\s*[:=]\s*["']?)([^\s"',;}]{6,})`), `${1}` + Placeholder},
}

// String returns s with any recognized secrets replaced by Placeholder.
// When masking is disabled it returns s unchanged.
func String(s string) string {
	if !enabled.Load() {
		return s
	}
	// Exact user-registered secrets first, then the built-in patterns.
	s = replaceSecrets(s)
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Writer wraps an io.Writer, masking secrets from streamed output.
// It buffers partial lines and masking each complete line, so a secret can't slip through by spanning two Write calls.
// Call Flush to emit a trailing partial line.
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	buf []byte
}

// NewWriter wraps w with secret masking.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (rw *Writer) Write(p []byte) (int, error) {
	// When disabled, pass through directly - no buffering, so output stays live.
	if !enabled.Load() {
		return rw.w.Write(p)
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.buf = append(rw.buf, p...)
	for {
		i := bytes.IndexByte(rw.buf, '\n')
		if i < 0 {
			break
		}
		if _, err := io.WriteString(rw.w, String(string(rw.buf[:i+1]))); err != nil {
			return 0, err
		}
		rw.buf = rw.buf[i+1:]
	}
	return len(p), nil
}

// Flush masking and writes any buffered partial line.
func (rw *Writer) Flush() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if len(rw.buf) == 0 {
		return nil
	}
	_, err := io.WriteString(rw.w, String(string(rw.buf)))
	rw.buf = nil
	return err
}
