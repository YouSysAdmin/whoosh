package executor

import (
	"bytes"
	"io"
	"log/slog"
	"sync"

	"github.com/yousysadmin/whoosh/internal/masking"
)

// captureSink buffers a silent_output task's output instead of streaming it, to be flushed only if the task fails.
// In raw mode the swapped e.out/cluster writer accumulates host-prefixed bytes in text; in log mode logLine/logExec
// append the structured records they would otherwise emit. A single task uses exactly one of the two.
type captureSink struct {
	mu      sync.Mutex
	text    bytes.Buffer // raw-mode output (host-prefixed, not yet masked - masking happens at flush via prevOut)
	records []captureRec // log-mode output: the slog calls deferred until flush
	prevOut io.Writer    // the writer e.out/cluster pointed at before capture, restored on endCapture
}

// captureRec is one deferred slog emission (an "output" or "exec" record) held during log-mode capture.
type captureRec struct {
	msg  string
	args []any
}

// Write accumulates raw-mode output; it is the io.Writer the executor swaps e.out and the cluster onto during capture.
func (s *captureSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.Write(p)
}

// add records a deferred log-mode emission.
func (s *captureSink) add(msg string, args []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, captureRec{msg: msg, args: args})
}

// beginCapture diverts the task's output into a fresh sink. Raw output is redirected via e.out and the cluster writer;
// log-mode output is captured because logLine/logExec consult e.capture. The caller must pair this with endCapture.
func (e *Executor) beginCapture() *captureSink {
	sink := &captureSink{prevOut: e.out}
	e.out = sink
	e.cluster.SetOut(sink)
	e.capture = sink
	return sink
}

// endCapture restores the previous output and either discards the captured output (task succeeded) or flushes it
// (task failed): raw bytes go through prevOut (the masking writer, so they are redacted and any colored prefix is
// preserved), log records are replayed via slog in order.
func (e *Executor) endCapture(sink *captureSink, runErr error) {
	e.out = sink.prevOut
	e.cluster.SetOut(sink.prevOut)
	e.capture = nil
	if runErr == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if e.logMode {
		for _, r := range sink.records {
			slog.Info(r.msg, r.args...)
		}
		return
	}
	if sink.text.Len() == 0 {
		return
	}
	_, _ = sink.prevOut.Write(sink.text.Bytes())
	if rw, ok := sink.prevOut.(*masking.Writer); ok {
		_ = rw.Flush()
	}
}
