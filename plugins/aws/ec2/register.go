package ec2

import (
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/yousysadmin/whoosh"
)

// Feature names - the `actions:` entry names and the namespace prefix of the actions each one registers
// ("<feature>:<action>").
const (
	FeatureInventory = "aws:ec2:inventory"
	FeatureASG       = "aws:ec2:asg"
	FeatureAMI       = "aws:ec2:ami"
)

// Register wires the EC2-family features onto clients built from the shared AWS config: the asg and ami actions are
// always available (their behavior comes from each task's `with:`), while the ec2:inventory startup hook runs only
// when configured (it needs tag filters, so we never auto-query the whole account). fp holds the per-feature params
// keyed by feature name. Implementations live in asg.go, ami.go and inventory.go.
func Register(reg *whoosh.Registry, cfg awssdk.Config, fp map[string]map[string]any) error {
	ec2c := awsec2.NewFromConfig(cfg)
	asgc := autoscaling.NewFromConfig(cfg)

	asg := &asgPlugin{api: asgc, ec2: ec2c, defaults: fp[FeatureASG], pollInterval: asgPollInterval}
	if err := reg.AddAction(actionASGRefresh, asg.runRefresh); err != nil {
		return err
	}
	if err := reg.AddAction(actionASGRollback, asg.runRollback); err != nil {
		return err
	}

	ami := &amiPlugin{ec2: ec2c, asg: asgc, defaults: fp[FeatureAMI], pollInterval: amiPollInterval}
	if err := reg.AddAction(actionAMICreate, ami.runCreate); err != nil {
		return err
	}
	if err := reg.AddAction(actionAMICleanup, ami.runCleanup); err != nil {
		return err
	}

	if p, ok := fp[FeatureInventory]; ok {
		var ip ec2InventoryParams
		if err := whoosh.DecodeParams(p, &ip); err != nil {
			return err
		}
		reg.AddStartup((&ec2Inventory{api: ec2c, params: ip}).appendHosts)
	}
	return nil
}
