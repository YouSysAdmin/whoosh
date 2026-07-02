package executor

import (
	"context"
	"fmt"
	"io"
	pathpkg "path"
	"strings"

	"github.com/yousysadmin/whoosh/internal/shtmpl"
	"github.com/yousysadmin/whoosh/internal/transport/local"
)

// writeFileCmd produces a shell command that creates path's parent and writes content to it with mode 0600 (umask 077),
// via printf with the content shell- quoted so any bytes are written verbatim.
// Used to render a generated file (e.g. an SSM env file) onto a host.
func writeFileCmd(path string, content []byte) string {
	return shtmpl.MustRender(shellTmpl, "write_file.sh.tmpl", struct {
		Dir, Path, Content string
	}{Dir: pathpkg.Dir(path), Path: path, Content: string(content)})
}

// DefaultInterpreter runs a script when none is specified.
const DefaultInterpreter = "/bin/sh"

// runLocalShell executes a command on the operator's machine, streaming output to out.
// Working dir and env are baked into the command (wrapRemote / buildScriptCommand), so this runs a bare shell.
// It reuses the local transport so there is one definition of "run a shell command locally": a task with local:true
// runs here directly (it has no inventory host), while a server with local:true reaches the same transport through the
// cluster.
func runLocalShell(ctx context.Context, command string, out io.Writer) error {
	if err := local.New().Run(ctx, command, out, out); err != nil {
		return fmt.Errorf("local command failed: %w", err)
	}
	return nil
}

// WrapCommand prepares a literal command for remote execution the way task cmds are: it exports env (expandable,
// sorted) and runs the command inside dir.
// It is exposed so the ad-hoc `run` command behaves like a task - same env and release directory.
// An empty dir or nil env omits that part.
func WrapCommand(command, dir string, env map[string]string) string {
	return wrapRemote(command, dir, env)
}

// wrapRemote prefixes a command with environment exports and a directory change, producing a single shell command
// suitable for one SSH session. Env keys are emitted in sorted order (text/template ranges maps by key).
func wrapRemote(command, dir string, env map[string]string) string {
	return shtmpl.MustRender(shellTmpl, "wrap_remote.sh.tmpl", struct {
		Env     map[string]string
		Dir     string
		Command string
	}{Env: env, Dir: dir, Command: command})
}

// buildScriptCommand produces a single shell command that exports env, optionally changes directory, and feeds the
// script content to the interpreter via a quoted heredoc (content passed verbatim, env inherited at run time).
// This works identically over SSH and locally, for file and inline scripts.
func buildScriptCommand(interpreter, content, dir string, env map[string]string) string {
	if interpreter == "" {
		interpreter = DefaultInterpreter
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return shtmpl.MustRender(shellTmpl, "script.sh.tmpl", struct {
		Env         map[string]string
		Dir         string
		Interpreter string
		Content     string
	}{Env: env, Dir: dir, Interpreter: interpreter, Content: content})
}
