package aws

import (
	"testing"

	"github.com/yousysadmin/whoosh"
)

func awsStaticParams() map[string]any {
	return map[string]any{"region": "us-east-1", "access_key_id": "AKIAEXAMPLE", "secret_access_key": "secret"}
}

func TestAWSPlugin_Version(t *testing.T) {
	v, ok := whoosh.Plugin(&awsPlugin{}).(whoosh.Versioner)
	if !ok {
		t.Fatal("awsPlugin does not implement whoosh.Versioner")
	}
	if v.Version() == "" {
		t.Fatal("awsPlugin.Version() is empty")
	}
}

func TestAWSPlugin_RegistersActionsAndInventory(t *testing.T) {
	reg, err := whoosh.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := whoosh.PluginSpec{
		Name:   "aws",
		Params: awsStaticParams(),
		Actions: []whoosh.PluginActionSpec{
			{Name: "aws:ec2:inventory", Params: map[string]any{"tags": map[string]any{"App": []any{"x"}}}},
			{Name: "aws:ec2:asg"},
			{Name: "aws:ec2:ami"},
		},
	}
	if err := (&awsPlugin{}).Configure(spec, reg); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	for _, a := range []string{"aws:ec2:asg:refresh", "aws:ec2:asg:rollback", "aws:ec2:ami:create", "aws:ec2:ami:cleanup", "aws:ssm:to-dotenv"} {
		if _, ok := reg.Action(a); !ok {
			t.Errorf("action %q not registered", a)
		}
	}
}

func TestAWSPlugin_ActionsAvailableWithoutListing(t *testing.T) {
	reg, _ := whoosh.Load(nil)
	spec := whoosh.PluginSpec{Name: "aws", Params: awsStaticParams()}
	if err := (&awsPlugin{}).Configure(spec, reg); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if _, ok := reg.Action("aws:ec2:ami:create"); !ok {
		t.Error("aws:ec2:ami:create should be available just by adding the plugins (no actions: needed)")
	}
}

func TestAWSPlugin_RejectsUnknownAction(t *testing.T) {
	reg, _ := whoosh.Load(nil)
	spec := whoosh.PluginSpec{Name: "aws", Params: awsStaticParams(), Actions: []whoosh.PluginActionSpec{{Name: "aws:bogus"}}}
	if err := (&awsPlugin{}).Configure(spec, reg); err == nil {
		t.Fatal("expected error for an unknown aws action")
	}
}
