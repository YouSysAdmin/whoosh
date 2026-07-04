package plugintemplate

// The three config surfaces a plugin decodes, most specific wins:
//
//	plugins[].params            -> globalParams (this file), decoded once in Configure
//	plugins[].actions[].params  -> per-feature params (inventory.go / context.go / setup.go) or action defaults
//	tasks[].with                -> per-call action params, layered over the action's defaults via decodeFeature
//
// All three arrive as map[string]any and decode into plain structs with yaml tags (whoosh.DecodeParams).
// String values in `with:` are Go-templated by the executor before they reach the plugin.

import (
	"github.com/yousysadmin/whoosh"
)

// globalParams is the plugin's `params:` block - the shared config every feature and action builds on.
// TODO: replace endpoint/token with your real connection config (region, URL, credentials file, ...).
type globalParams struct {
	// Endpoint of the external system this plugin talks to (see client.go).
	Endpoint string `yaml:"endpoint"`
	// Token authenticates against it. Configure registers the literal with whoosh.AddSecret, so it is masked in every
	// echoed command, output line, log record, and dry-run plan. Feed it from the operator env:
	//	token: '{{ env "EXAMPLE_TOKEN" }}'
	Token string `yaml:"token"`
}

// validate is the offline gate: everything checkable without network/config belongs here, so `whoosh validate`
// catches it. Endpoint is deliberately NOT required here - only the features that use the client need it, and they
// check at use time with a clear error.
// TODO: validate your real params (formats, enums, mutually-exclusive fields, ...).
func (p globalParams) validate() error {
	return nil
}

// merge layers over on top of base: nested maps merge recursively, scalars and slices replace.
// Neither input is mutated. This is the layering primitive behind decodeFeature.
func merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = merge(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// decodeFeature decodes an action call's effective params: the task's `with:` layered over the feature's
// `actions:` defaults (with: wins). Both maps may be nil.
func decodeFeature(defaults, with map[string]any, out any) error {
	return whoosh.DecodeParams(merge(defaults, with), out)
}
