package cli

import (
	"testing"

	"github.com/spf13/cobra"

	// Register the default-on print-hosts-table plugin so its deploy:hosts command is discoverable here, exercising the
	// plugins-command wiring end to end.
	_ "github.com/yousysadmin/whoosh/plugins/standard/print_hosts_table"
)

func TestHostsNoLongerReserved(t *testing.T) {
	if reservedActions["hosts"] {
		t.Error("`hosts` should no longer be a built-in reserved action (moved to the print-hosts-table plugins as deploy:hosts)")
	}
}

// registerPluginCmds should surface the print-hosts-table plugin's deploy:hosts command as a stage subcommand,
// discovered offline (no Deployfile needed - the default-on plugins is included via the fallback in activePluginSpecs).
func TestRegisterPluginCmds_AddsDeployHosts(t *testing.T) {
	stageCmd := &cobra.Command{Use: "production"}
	registerPluginCmds(stageCmd, "production", &globalFlags{})

	if !hasSubcommand(stageCmd, "deploy:hosts") {
		var names []string
		for _, c := range stageCmd.Commands() {
			names = append(names, c.Name())
		}
		t.Fatalf("deploy:hosts not registered, subcommands: %v", names)
	}
}

// A plugins command must not shadow a built-in action or an existing subcommand.
func TestRegisterPluginCmds_SkipsCollisions(t *testing.T) {
	stageCmd := &cobra.Command{Use: "production"}
	stageCmd.AddCommand(&cobra.Command{Use: "deploy:hosts"}) // pre-existing (e.g. a task)
	before := len(stageCmd.Commands())
	registerPluginCmds(stageCmd, "production", &globalFlags{})
	if got := len(stageCmd.Commands()); got != before {
		t.Errorf("registerPluginCmds added a colliding command: %d -> %d", before, got)
	}
}
