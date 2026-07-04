package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

// checkTemplates render-checks every user template in the config: global and per-task `envs:`, `cmds`, `dir:`,
// action `with:` params, inline scripts, and file scripts (existence always, rendered when templated - `.tmpl`
// suffix or `template: true`).
// It runs as the last step of loadOffline - i.e. on every config load, for every command.
func checkTemplates(cfg *ast.DeployFile) []error {
	ctx := validateContext(cfg)
	render := func(text string) error {
		_, err := varstmpl.RenderWith(text, ctx, false)
		return err
	}

	var errs []error
	check := func(what, text string) {
		if err := render(text); err != nil {
			errs = append(errs, fmt.Errorf("%s: %s", what, conciseErr(err)))
		}
	}

	for _, k := range sortedKeys(cfg.Envs) {
		check(fmt.Sprintf("envs.%s", k), cfg.Envs[k])
	}

	scriptsDir := deployfile.ScriptsLocation(cfg)
	for _, name := range sortedKeys(cfg.Tasks) {
		t := cfg.Tasks[name]
		if t == nil || !t.ActiveForStage(cfg.Stage) {
			continue
		}
		for _, k := range sortedKeys(t.Envs) {
			check(fmt.Sprintf("task %q envs.%s", name, k), t.Envs[k])
		}
		for i, c := range t.Cmds {
			check(fmt.Sprintf("task %q cmds[%d]", name, i), c)
		}
		if t.Dir != "" {
			check(fmt.Sprintf("task %q dir", name), t.Dir)
		}
		if len(t.With) > 0 {
			if _, err := varstmpl.RenderParams(t.With, ctx, false); err != nil {
				errs = append(errs, fmt.Errorf("task %q with: %s", name, conciseErr(err)))
			}
		}
		for _, sc := range t.Scripts {
			label := scriptRef(sc)
			if sc.Script != "" { // inline scripts are always templated
				check(fmt.Sprintf("task %q script %s", name, label), sc.Script)
				continue
			}
			if sc.Path == "" {
				continue
			}
			full := sc.Path
			if !filepath.IsAbs(full) {
				full = filepath.Join(scriptsDir, full)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				errs = append(errs, fmt.Errorf("task %q script %s: %w", name, label, err))
				continue
			}
			if sc.Template || strings.HasSuffix(sc.Path, ".tmpl") {
				check(fmt.Sprintf("task %q script %s", name, label), string(data))
			}
		}
	}
	return errs
}

// reportTemplateFindings prints the findings as a plain list on w (redacted - a finding could quote a secret-bearing
// value) and returns a summary error carrying only the count, nil when there are none.
// Shared by `validate` and loadConfig.
func reportTemplateFindings(w io.Writer, findings []error) error {
	if len(findings) == 0 {
		return nil
	}
	out := masking.NewWriter(w)
	fmt.Fprintln(out, "template check failed:")
	for _, f := range findings {
		fmt.Fprintf(out, "  - %v\n", f)
	}
	out.Flush()
	return fmt.Errorf("template check: %d problem(s)", len(findings))
}

// conciseErr reduces a varstmpl render error to its cause for a findings line: the wrapping layers (the template text,
// text/template's "template: cmd:" location prefix) repeat what the finding label already says, so only the innermost
// message is kept, with a template-relative line number where the parser provided one.
func conciseErr(err error) string {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			break
		}
		err = inner
	}
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "template: cmd:"); ok {
		msg = "line " + rest
	}
	return msg
}

// validateContext is the lenient template context validate renders against: the real load-time values (resolved vars,
// app/stage/paths, env/env_files, {{.config}}) plus non-empty placeholders for the run-time keys, so a `required`
// guard on e.g. {{.commit_hash}} - always set during a real deploy - doesn't false-fail offline.
func validateContext(cfg *ast.DeployFile) varstmpl.Context {
	// Plugin imports ({{ .ssm.* }}, ...) and dynamic inventory don't exist yet - the check runs before plugins load -
	// so those keys resolve through the lenient missingkey handling, a `required` guard on an import value can't be
	// verified offline and would fail here, guard imports at run time instead (e.g. in the script).
	ctx := loadTimeContext(cfg)
	ctx.KeepReleases = cfg.App.KeepReleases
	ctx.Config, _ = cfg.AsMap()
	ctx.ReleasePath = ctx.CurrentPath
	ctx.ReleaseTimestamp = "19700101000000"
	ctx.CommitHash = "0000000000000000000000000000000000000000"
	ctx.Host = "validate-host"
	if hosts := ast.FilterDeployable(cfg.Hosts); len(hosts) > 0 {
		ctx.Host = hosts[0].Address
		ctx.Roles = hosts[0].Roles
	}
	return ctx
}

func scriptRef(sc ast.Script) string {
	if sc.Path != "" {
		return sc.Path
	}
	if sc.Name != "" {
		return sc.Name
	}
	return "inline"
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
