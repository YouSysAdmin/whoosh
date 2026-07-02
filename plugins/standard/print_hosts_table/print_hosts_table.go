// Package print_hosts_table is a zero-config standard plugins that prints the resolved inventory (the hosts table) at
// the start of every deploy.
// It is registered as a default-on plugins (whoosh.RegisterDefault), so it is active without being listed under
// plugins:, disable it per stage with:
//
//	plugins:
//	  - name: print-hosts-table
//	    enabled: false
//
// It contributes two things, both rendering the same table:
//   - a phase func-hook (cfg.AddHookFuncBefore) that prints the hosts table to the
//     deploy's console before the deploy:starting phase - no task or shell echo. Func
//     hooks run only during the deploy lifecycle (on `deploy`, not config/run).
//   - the `whoosh <stage> deploy:hosts` CLI command (via whoosh.Commander), the
//     on-demand replacement for the former built-in `hosts` command.
package print_hosts_table

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/yousysadmin/whoosh"
)

const (
	pluginName    = "print-hosts-table"
	pluginVersion = "1.0.0"
)

func init() {
	whoosh.RegisterDefault(pluginName, func() whoosh.Plugin { return &plugin{} })
}

type plugin struct{}

// Version reports the plugin's version (whoosh.Versioner), shown by `whoosh plugins` / `whoosh version`.
func (p *plugin) Version() string { return pluginVersion }

// Configure registers the startup hook, the plugin takes no params or actions.
func (p *plugin) Configure(_ whoosh.PluginSpec, reg *whoosh.Registry) error {
	reg.AddStartup(p.install)
	return nil
}

// install wires a func-hook to run before deploy:starting that prints the resolved hosts table straight to the deploy's
// console.
// The closure reads cfg.Hosts when it runs (after startup inventory is resolved), and renders the same table the
// `deploy:hosts` command prints.
func (p *plugin) install(_ context.Context, cfg *whoosh.DeployFile) error {
	cfg.AddHookFuncBefore(whoosh.PhaseStarting, func(_ context.Context, out io.Writer) error {
		if strings.EqualFold(cfg.Log.Format, "json") {
			slog.Info("hosts", slog.Any("hosts", cfg.Hosts))
			return nil
		}
		return printHostsTable(out, cfg.Hosts)
	})
	return nil
}

// Commands contributes the `whoosh <stage> deploy:hosts` CLI command - the on-demand counterpart to the auto-print
// func-hook above, and the replacement for the former built-in `hosts` command.
// Implementing this (the whoosh.Commander interface) is what registers the subcommand.
func (p *plugin) Commands() []whoosh.Command {
	return []whoosh.Command{{
		Name:  "deploy:hosts",
		Short: "Print the stage's hosts as a table",
		Run: func(_ context.Context, cfg *whoosh.DeployFile, _ *whoosh.Registry, out io.Writer, _ []string) error {
			if strings.EqualFold(cfg.Log.Format, "json") {
				slog.Info("hosts", slog.Any("hosts", cfg.Hosts))
				return nil
			}
			return printHostsTable(out, cfg.Hosts)
		},
	}}
}

// Output table of hosts
func printHostsTable(w io.Writer, hosts []whoosh.Host) error {
	_, err := fmt.Fprint(w, hostsTable(hosts))
	return err
}

// hostsTable renders every host as a bordered text table - address, roles, deploy flag, transport, and source -
// including deploy:false hosts. It backs the `deploy:hosts` command and the deploy-time auto-print.
func hostsTable(hosts []whoosh.Host) string {
	rows := make([][]string, 0, len(hosts))
	for _, h := range hosts {
		deploy := "no"
		if h.DeployEnabled() {
			deploy = "yes"
		}
		transport := "ssh"
		if h.Local {
			transport = "local"
		}
		roles := strings.Join(h.Roles, ",")
		if roles == "" {
			roles = "-"
		}
		// Where the host came from: "config" (Deployfile) or a plugin feature (e.g. "aws:ec2:inventory").
		// Defaulted here in case the table is rendered before ApplyDefaults stamped it.
		source := h.Source
		if source == "" {
			source = whoosh.HostSourceConfig
		}
		rows = append(rows, []string{h.Address, roles, deploy, transport, source})
	}

	// Just print ASCII table
	// see https://github.com/olekukonko/tablewriter for more info
	var b strings.Builder
	table := tablewriter.NewWriter(&b)
	defer table.Close()
	table.Header([]string{"HOST", "ROLES", "DEPLOY", "TRANSPORT", "SOURCE"})
	err := table.Bulk(rows)
	if err != nil {
		slog.Error("generate table of hosts", "error", err.Error())
	}
	err = table.Render()
	if err != nil {
		slog.Error("render table of hosts", "error", err.Error())
	}
	return b.String()
}
