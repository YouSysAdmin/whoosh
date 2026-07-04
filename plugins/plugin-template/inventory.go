package plugintemplate

// The inventory feature: a startup hook that discovers hosts from the external system and appends them to the
// resolved config's host list - dynamic inventory, same shape as aws:ec2:inventory. Startup hooks run on EVERY
// command that loads the config (deploy, run, config, even --dry-run), because they produce the host list.
//
// Opt-in: runs only when the Deployfile lists it -
//
//	plugins:
//	  - name: plugin-template
//	    actions:
//	      - name: plugin-template:inventory
//	        params: { roles: [app], user: deploy, deploy: true }

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yousysadmin/whoosh"
)

// FeatureInventory is the feature's name in the `actions:` list.
const FeatureInventory = pluginName + ":inventory"

// inventoryParams shapes the hosts the discovery appends.
// TODO: add your real discovery filters (tags, labels, a query, ...).
type inventoryParams struct {
	// Roles / User / Port are stamped on every discovered host.
	Roles []string `yaml:"roles"`
	User  string   `yaml:"user"`
	Port  int      `yaml:"port"`
	// Deploy / Required map to the host flags: deploy:false = inventory-only (excluded from execution),
	// required:true = an unreachable host aborts even under on_unreachable: skip. Nil leaves the defaults.
	Deploy   *bool `yaml:"deploy"`
	Required *bool `yaml:"required"`
}

// decodeInventoryParams decodes+validates the feature's params at Configure time (offline).
func decodeInventoryParams(raw map[string]any) (inventoryParams, error) {
	var fp inventoryParams
	if err := whoosh.DecodeParams(raw, &fp); err != nil {
		return fp, fmt.Errorf("%s params: %w", FeatureInventory, err)
	}
	return fp, nil
}

// inventoryStartup returns the StartupFunc that runs the discovery and mutates cfg.Hosts.
func (p *plugin) inventoryStartup(fp inventoryParams) whoosh.StartupFunc {
	return func(ctx context.Context, cfg *whoosh.DeployFile) error {
		if err := p.client.requireEndpoint(FeatureInventory); err != nil {
			return err
		}
		addrs, err := p.client.discover(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", FeatureInventory, err)
		}
		for _, addr := range addrs {
			cfg.Hosts = append(cfg.Hosts, whoosh.Host{
				Address:  addr,
				Roles:    fp.Roles,
				User:     fp.User,
				Port:     fp.Port,
				Deploy:   fp.Deploy,
				Required: fp.Required,
				// Source tells the hosts table where a host came from: our feature name, vs whoosh.HostSourceConfig
				// for Deployfile-declared hosts.
				Source: FeatureInventory,
			})
		}
		// Plugin narrative goes through slog, only command output goes to the console writer.
		slog.Info("inventory discovered hosts", "plugin", pluginName, "count", len(addrs))
		return nil
	}
}
