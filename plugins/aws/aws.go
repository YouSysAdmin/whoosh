// Package aws provides the compiled-in `aws` whoosh plugins, backed by the AWS SDK.
// Listing the plugin once activates all its features, they share one AWS connection (region + credentials), resolved
// from the plugin's global params (see credentials.go):
//
//	plugins:
//	  - name: aws
//	    params:                       # global: region + a credential source
//	      credentials_from_host: { host: "{{ .bastion }}", user: deployer }
//	    actions:                      # per-feature config (layered on the global params)
//	      - name: aws:ec2:inventory   # startup, needs tag filters, so only runs when listed
//	        params: { tags: { ... } }
//	      - name: aws:ec2:asg             # actions are available whether listed or not
//	      - name: aws:ec2:ami
//
// Each service lives in its own sub-package, which exports a Register function and
// its feature name(s):
//   - ec2 (ec2/) - aws:ec2:inventory (a startup hook that appends tag-selected EC2
//     instances to Servers), aws:ec2:asg (refresh/rollback) and aws:ec2:ami
//     (create/cleanup).
//   - ssm (ssm/) - aws:ssm:to-dotenv: fetch SSM parameters by path prefix and write
//     them to a dotenv file (and inject them into the template context).
//   - secretstore (secretstore/) - aws:secrets:to-dotenv: the Secrets Manager
//     counterpart (JSON-object secrets expand to one var per key).
//
// Configure resolves the shared AWS config once and hands it to each sub-package's
// Register, which builds its own client(s) from it. The asg/ami actions are
// available whenever the plugin is loaded, their behavior comes from each task's
// `with:`. Credentials are global only - per-feature `params:` carry feature config
// (e.g. inventory tags), not credentials. Param merging (feature defaults under a
// task's `with:`) is shared via the internal/params package.
package aws

import (
	"context"
	"fmt"

	"github.com/yousysadmin/whoosh"
	"github.com/yousysadmin/whoosh/plugins/aws/ec2"
	"github.com/yousysadmin/whoosh/plugins/aws/secretstore"
	"github.com/yousysadmin/whoosh/plugins/aws/ssm"
)

// pluginAWS is the single registered plugin name, it is also the namespace prefix of every action it registers
// ("aws:..."), which the core uses to skip an action task when the plugin is inactive for a stage.
const pluginAWS = "aws"

// pluginVersion is reported via whoosh.Versioner and shown by `whoosh plugins` / `whoosh version`. The aws plugin is a
// separate module, so it carries its own version.
const pluginVersion = "1.0.0"

func init() {
	whoosh.Register(pluginAWS, func() whoosh.Plugin { return &awsPlugin{} })
}

// awsPlugin is the umbrella plugin.
// It builds the shared AWS connection once from the global params and registers every feature's actions/startup hooks.
type awsPlugin struct{}

// Version reports the plugin's version (whoosh.Versioner).
func (a *awsPlugin) Version() string { return pluginVersion }

// Configure resolves the shared AWS connection from the global params, then hands the config to each service's Register
// (each builds its own client from it).
func (a *awsPlugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var c awsConfig
	if err := whoosh.DecodeParams(spec.Params, &c); err != nil {
		return err
	}
	cfg, err := loadAWS(context.Background(), c)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	fp, err := indexFeatures(spec.Actions)
	if err != nil {
		return err
	}
	if err := ec2.Register(reg, cfg, fp); err != nil {
		return err
	}
	if err := ssm.Register(reg, cfg, fp); err != nil {
		return err
	}
	return secretstore.Register(reg, cfg, fp)
}

// indexFeatures validates each `actions:` entry against the known feature names (rejecting an unknown one early) and
// returns their params keyed by feature name, for each sub-package's Register to consume.
func indexFeatures(actions []whoosh.PluginActionSpec) (map[string]map[string]any, error) {
	fp := map[string]map[string]any{}
	for _, act := range actions {
		switch act.Name {
		case ec2.FeatureInventory, ec2.FeatureASG, ec2.FeatureAMI, ssm.Feature, secretstore.Feature:
			fp[act.Name] = act.Params
		default:
			return nil, fmt.Errorf("unknown aws action %q (want %q, %q, %q, %q, or %q)",
				act.Name, ec2.FeatureInventory, ec2.FeatureASG, ec2.FeatureAMI, ssm.Feature, secretstore.Feature)
		}
	}
	return fp, nil
}
