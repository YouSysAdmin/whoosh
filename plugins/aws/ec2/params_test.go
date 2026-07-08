package ec2

import (
	"testing"

	"github.com/yousysadmin/whoosh"
	"github.com/yousysadmin/whoosh/plugins/aws/internal/params"
)

// The inventory params decode through the same path Register uses, including the resolve_config_hosts flag.
func TestDecodeInventoryParams(t *testing.T) {
	var ip ec2InventoryParams
	err := whoosh.DecodeParams(map[string]any{
		"tags":                 map[string]any{"Environment": "uat"},
		"roles":                []any{"app"},
		"resolve_config_hosts": true,
	}, &ip)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if !ip.ResolveConfigHosts {
		t.Error("resolve_config_hosts should decode to true")
	}
	if len(ip.Tags["Environment"]) != 1 || ip.Tags["Environment"][0] != "uat" {
		t.Errorf("tags = %v", ip.Tags)
	}
}

func TestDecodeFeatureParams_DefaultsThenWith(t *testing.T) {
	// Feature-level defaults (the plugins `aws:ec2:ami` actions: entry) decode under the task `with:`: the task overrides
	// name_prefix, adds a source tag, and the default launch_template is preserved.
	defaults := map[string]any{
		"name_prefix":     "default-prefix",
		"source_tags":     map[string]any{"Application": "managebac"},
		"launch_template": map[string]any{"asg": "my-asg"},
	}
	with := map[string]any{
		"name_prefix": "task-prefix",
		"source_tags": map[string]any{"Role": "AMICreator"},
	}

	var ap amiCreateParams
	if err := params.DecodeFeature(defaults, with, &ap); err != nil {
		t.Fatalf("DecodeFeature: %v", err)
	}
	if ap.NamePrefix != "task-prefix" {
		t.Errorf("NamePrefix = %q, want task-prefix (task wins)", ap.NamePrefix)
	}
	if ap.SourceTags["Application"] != "managebac" || ap.SourceTags["Role"] != "AMICreator" {
		t.Errorf("SourceTags = %v, want {Application:managebac, Role:AMICreator}", ap.SourceTags)
	}
	if ap.LaunchTemplate == nil || ap.LaunchTemplate.ASG != "my-asg" {
		t.Errorf("LaunchTemplate = %+v, want default {asg: my-asg}", ap.LaunchTemplate)
	}

	// With no task `with:`, the defaults stand alone.
	var only amiCreateParams
	if err := params.DecodeFeature(defaults, nil, &only); err != nil {
		t.Fatalf("DecodeFeature(defaults, nil): %v", err)
	}
	if only.NamePrefix != "default-prefix" || only.SourceTags["Application"] != "managebac" {
		t.Errorf("defaults-only = %+v", only)
	}
}
