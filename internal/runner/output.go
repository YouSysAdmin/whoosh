package runner

import (
	"bytes"
	"io"
	"sync"
)

// LineHandler receives one complete line of command output at a time, tagged with its source host (the trailing
// newline trimmed). A Cluster with a handler set routes output through it instead of writing host-prefixed text to its
// writer, so command output can be emitted as structured log records rather than streamed raw.
type LineHandler func(host, line string)

// ANSI color codes for the host output prefix.
const (
	ansiGreen = "\033[32m"
	ansiReset = "\033[0m"
)

// HostLabel formats a host's "[host]" output prefix. When color is set the label is wrapped in green (it marks which
// host the line came from), otherwise it is returned plain. Both stdout and stderr use the same color: the prefix is a
// host identity, not a severity - many tools (git, npm, ...) write normal progress to stderr, and the line streams
// before the command's exit status is known, so the prefix can't reliably signal failure. A failed command still
// surfaces in red through the slog narrative. There is no trailing space, callers add their own separator.
func HostLabel(host string, color bool) string {
	label := "[" + host + "]"
	if !color {
		return label
	}
	return ansiGreen + label + ansiReset
}

// LineWriter writes command output one complete line at a time, buffering partial writes until a newline arrives.
// In raw mode it writes "<prefix><line>" to an underlying writer, with a LineHandler it instead hands each complete
// line to the handler tagged with the source host - the seam for routing output through structured logging.
// A single mutex shared across the writers targeting one raw destination keeps concurrent hosts from interleaving
// mid-line, a log writer locks only itself, since each line becomes its own (atomic) log record.
type LineWriter struct {
	w       io.Writer
	prefix  string
	host    string
	handler LineHandler
	mu      sync.Locker
	buf     []byte
}

// newPrefixWriter builds a writer that streams complete lines to w, each prefixed with the host label, serialized
// against the shared mutex.
func newPrefixWriter(w io.Writer, prefix string, mu *sync.Mutex) *LineWriter {
	return &LineWriter{w: w, prefix: prefix, mu: mu}
}

// NewLogWriter builds a writer that hands each complete line to handler tagged with host, instead of writing raw text.
// It locks only itself (each line is an independent log record), so callers need not share a mutex.
func NewLogWriter(host string, handler LineHandler) *LineWriter {
	return &LineWriter{host: host, handler: handler, mu: &sync.Mutex{}}
}

// NewPrefixWriter builds a writer that streams complete lines to w, each with the given prefix (typically a HostLabel
// plus a space). It owns its mutex - for a single-stream caller outside the cluster, e.g. a local task's output, which
// the cluster's internal shared-mutex variant doesn't cover.
func NewPrefixWriter(w io.Writer, prefix string) *LineWriter {
	return &LineWriter{w: w, prefix: prefix, mu: &sync.Mutex{}}
}

func (lw *LineWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.buf = append(lw.buf, p...)
	for {
		i := bytes.IndexByte(lw.buf, '\n')
		if i < 0 {
			break
		}
		if err := lw.emit(lw.buf[:i+1]); err != nil {
			return 0, err
		}
		lw.buf = lw.buf[i+1:]
	}
	return len(p), nil
}

// Flush writes any buffered partial line, adding a trailing newline.
func (lw *LineWriter) Flush() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.buf) == 0 {
		return
	}
	_ = lw.emit(append(lw.buf, '\n'))
	lw.buf = nil
}

// Close flushes any buffered partial line. It lets a LineWriter stand in for an io.WriteCloser.
func (lw *LineWriter) Close() error {
	lw.Flush()
	return nil
}

// emit outputs one complete, newline-terminated line: through the handler (newline trimmed) when set, else as
// "<prefix><line>" to the underlying writer.
func (lw *LineWriter) emit(line []byte) error {
	if lw.handler != nil {
		lw.handler(lw.host, string(bytes.TrimRight(line, "\n")))
		return nil
	}
	if _, err := io.WriteString(lw.w, lw.prefix); err != nil {
		return err
	}
	_, err := lw.w.Write(line)
	return err
}
