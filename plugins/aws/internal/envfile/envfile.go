// Package envfile writes rendered dotenv content where an aws to-dotenv action asked for it: on the task's hosts when
// the executor provided a host writer (the normal case), otherwise (e.g. a unit test, or no executor) on the operator
// machine, 0600 since the file holds secrets. Shared by the ssm and secretstore actions so the fallback and logging
// stay identical.
package envfile

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/yousysadmin/whoosh"
)

// Write writes content at path, hosts-first with the operator-side fallback. It logs hostMsg or localMsg (each
// caller's established wording) with the given slog attrs.
func Write(ctx context.Context, path string, content []byte, hostMsg, localMsg string, attrs ...any) error {
	if w := whoosh.HostFileWriterFrom(ctx); w != nil {
		if err := w.WriteFile(ctx, path, content); err != nil {
			return err
		}
		slog.Info(hostMsg, attrs...)
		return nil
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	slog.Info(localMsg, attrs...)
	return nil
}
