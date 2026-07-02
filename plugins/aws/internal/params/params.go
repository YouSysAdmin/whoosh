// Package params merges and decodes an aws-plugins action's effective parameters: the feature-level defaults (the
// plugin's `actions: - name: <feature>` params) with the task's `with:` layered on top (the task wins).
// Shared by the aws sub-packages (ec2, ssm, secretstore) so a feature can be configured once at the plugins level and
// overridden per task invocation.
package params

import "github.com/yousysadmin/whoosh"

// DecodeFeature decodes an action's effective params into target: the feature defaults with the task's `with:` layered
// on top (the task wins).
func DecodeFeature(defaults, with map[string]any, target any) error {
	return whoosh.DecodeParams(Merge(defaults, with), target)
}

// Or returns *p when set, else def - the default for an optional (*T) param left unset.
func Or[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}

// OrPtr returns p when set, else a pointer to def - for optional params handed to AWS SDK inputs, which take pointers.
func OrPtr[T any](p *T, def T) *T {
	if p != nil {
		return p
	}
	return &def
}

// Merge returns base with over layered on top (over wins).
// Nested map[string]any values are merged recursively, every other value (scalars, slices) is replaced wholesale by
// over. A nil base yields a copy of over, so an unconfigured feature behaves exactly as "task `with:` only".
// Inputs are not mutated.
func Merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = Merge(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}
