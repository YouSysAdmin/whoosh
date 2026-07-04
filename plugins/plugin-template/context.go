package plugintemplate

// The context feature: a startup hook that fetches values once at load and injects them into whoosh's template/env
// context - the pattern behind aws:ssm's startup. Each value becomes available to every task as
//
//	{{ .<namespace>.<key> }}     in templated cmds/scripts
//	$<NAMESPACE>_<KEY>           in the exported task environment
//
// and is registered as a secret so whoosh masks it in all output. Imports are yaml:"-" on the config, so they never
// leak into `whoosh <stage> config` / {{ .config }}.
//
// Opt-in:
//
//	plugins:
//	  - name: plugin-template
//	    actions:
//	      - name: plugin-template:context
//	        params: { keys: [db_url, api_key], namespace: template }

import (
	"context"
	"fmt"

	"github.com/yousysadmin/whoosh"
)

// FeatureContext is the feature's name in the `actions:` list.
const FeatureContext = pluginName + ":context"

// defaultNamespace is the import namespace when the params don't set one.
const defaultNamespace = "template"

// contextParams selects what to fetch and where to expose it.
type contextParams struct {
	// Keys are the values to fetch from the external system, one import per key.
	Keys []string `yaml:"keys"`
	// Namespace is the import namespace ({{ .<ns>.<key> }} / $<NS>_<KEY>). Default "template".
	Namespace string `yaml:"namespace"`
}

// decodeContextParams decodes+validates the feature's params at Configure time (offline).
func decodeContextParams(raw map[string]any) (contextParams, error) {
	var fp contextParams
	if err := whoosh.DecodeParams(raw, &fp); err != nil {
		return fp, fmt.Errorf("%s params: %w", FeatureContext, err)
	}
	if len(fp.Keys) == 0 {
		return fp, fmt.Errorf("%s: 'keys' is required (nothing to fetch)", FeatureContext)
	}
	if fp.Namespace == "" {
		fp.Namespace = defaultNamespace
	}
	return fp, nil
}

// contextStartup returns the StartupFunc that fetches every key and injects it into cfg.Imports.
func (p *plugin) contextStartup(fp contextParams) whoosh.StartupFunc {
	return func(ctx context.Context, cfg *whoosh.DeployFile) error {
		if err := p.client.requireEndpoint(FeatureContext); err != nil {
			return err
		}
		for _, key := range fp.Keys {
			value, err := p.client.fetch(ctx, key)
			if err != nil {
				return fmt.Errorf("%s: fetch %q: %w", FeatureContext, key, err)
			}
			// Mask first, then expose: the value is a secret the moment it exists.
			whoosh.AddSecret(value)
			cfg.AddImport(fp.Namespace, key, value)
		}
		return nil
	}
}
