package ec2

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/yousysadmin/whoosh"
	"gopkg.in/yaml.v3"
)

// ec2API is the slice of the EC2 client the inventory plugin uses.
type ec2API interface {
	DescribeInstances(ctx context.Context, in *awsec2.DescribeInstancesInput,
		optFns ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error,
	)
}

type ec2InventoryParams struct {
	Tags        map[string]stringList `yaml:"tags"`
	RoleTag     string                `yaml:"role_tag"`
	Roles       []string              `yaml:"roles"`
	UsePublicIP bool                  `yaml:"use_public_ip"`
	// DeployTag, when set, marks a discovered instance deploy:true only when it carries this tag (Name/Value), every other
	// matched instance is added with deploy:false - still listed in inventory, just not deployed to.
	// When unset, all discovered instances deploy (the default).
	DeployTag *tagMatch `yaml:"deploy_tag"`
	// RequiredTag, when set, marks a discovered instance required:true (its unreachability always aborts, even under
	// on_unreachable: skip) when it carries this tag. When unset, discovered hosts are not required.
	RequiredTag *tagMatch `yaml:"required_tag"`
	// ResolveConfigHosts, when set, resolves the already-listed hosts' FQDN addresses to IPs (on the operator's
	// machine) before the duplicate check, so a machine declared in the Deployfile by name and discovered by EC2 by IP
	// is not listed twice - the declared entry (with its roles and flags) wins. A failed lookup only logs a warning.
	ResolveConfigHosts bool `yaml:"resolve_config_hosts"`
}

// tagMatch is a single EC2 tag name/value used to derive a per-host flag.
type tagMatch struct {
	Name  string `yaml:"Name"`
	Value string `yaml:"Value"`
}

// stringList accepts either a single scalar or a sequence in YAML, always yielding a slice.
// It lets a tag filter be written `Environment: uat` or `Environment: [uat, staging]` (the list matches any of the
// values).
type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var many []string
		if err := node.Decode(&many); err != nil {
			return err
		}
		*s = many
		return nil
	}
	var single string
	if err := node.Decode(&single); err != nil {
		return err
	}
	*s = stringList{single}
	return nil
}

// ec2Inventory is the startup feature of the aws plugins: it appends running EC2 instances (matching the configured
// tags) to the deployment's Hosts. It is constructed by the umbrella plugin (aws.go) with a shared EC2 client.
// Listing the resolved inventory is the `deploy:hosts` command's job, not this feature's.
type ec2Inventory struct {
	api    ec2API
	params ec2InventoryParams
	// lookupHost resolves a hostname for the resolve_config_hosts dedup. nil means net.DefaultResolver - a field so
	// tests can inject a fake resolver.
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

// appendHosts queries EC2 and appends matching instances to cfg.Hosts.
// Roles come from role_tag (comma-separated) when set, otherwise the static roles list.
func (i *ec2Inventory) appendHosts(ctx context.Context, cfg *whoosh.DeployFile) error {
	filters := []ec2types.Filter{{
		Name:   awssdk.String("instance-state-name"),
		Values: []string{"running"},
	}}
	for k, vals := range i.params.Tags {
		if len(vals) == 0 {
			continue
		}
		filters = append(filters, ec2types.Filter{
			Name:   awssdk.String("tag:" + k),
			Values: vals,
		})
	}

	// Skip any instance whose address is already in the inventory. A statically
	// declared host (or one already appended from a prior reservation) wins over
	// discovery, so a host that is both listed in the Deployfile and returned by
	// the EC2 query is not duplicated with a conflicting deploy/roles entry.
	seen := make(map[string]bool, len(cfg.Hosts))
	for _, h := range cfg.Hosts {
		seen[h.Address] = true
	}
	// With resolve_config_hosts, a host declared by FQDN also blocks its resolved IPs, so the same machine
	// discovered by EC2 by IP is not listed twice. Resolution failures only warn - a stale DNS name must not fail
	// startup, it just leaves the potential duplicate visible.
	if i.params.ResolveConfigHosts {
		lookup := i.lookupHost
		if lookup == nil {
			lookup = net.DefaultResolver.LookupHost
		}
		for _, h := range cfg.Hosts {
			if h.Address == "" || net.ParseIP(h.Address) != nil {
				continue
			}
			ips, err := lookup(ctx, h.Address)
			if err != nil {
				slog.Warn("resolve host for inventory dedup failed", "host", h.Address, "error", err)
				continue
			}
			for _, ip := range ips {
				seen[ip] = true
			}
		}
	}

	// DescribeInstances is paginated (the SDK does not auto-paginate), so loop on NextToken -
	// a fleet spanning pages would otherwise be silently truncated.
	var nextToken *string
	for {
		out, err := i.api.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{Filters: filters, NextToken: nextToken})
		if err != nil {
			return fmt.Errorf("describe instances: %w", err)
		}

		for _, res := range out.Reservations {
			for _, inst := range res.Instances {
				addr := instanceHost(inst, i.params.UsePublicIP)
				if addr == "" || seen[addr] {
					continue
				}
				seen[addr] = true
				roles := i.params.Roles
				if i.params.RoleTag != "" {
					if v := tagValue(inst.Tags, i.params.RoleTag); v != "" {
						roles = splitRoles(v)
					}
				}
				h := whoosh.Host{Address: addr, Roles: roles, Source: FeatureInventory}
				// With deploy_tag set, only tag-matching instances deploy, the rest are still listed (deploy:false).
				// Without it, all instances deploy.
				if dt := i.params.DeployTag; dt != nil {
					h.Deploy = new(tagValue(inst.Tags, dt.Name) == dt.Value)
				}
				// With required_tag set, tag-matching instances are required (never skipped on unreachability).
				if rt := i.params.RequiredTag; rt != nil {
					h.Required = new(tagValue(inst.Tags, rt.Name) == rt.Value)
				}
				cfg.Hosts = append(cfg.Hosts, h)
			}
		}

		if out.NextToken == nil {
			return nil
		}
		nextToken = out.NextToken
	}
}

func instanceHost(inst ec2types.Instance, public bool) string {
	if public {
		if inst.PublicIpAddress != nil {
			return *inst.PublicIpAddress
		}
		return ""
	}
	if inst.PrivateIpAddress != nil {
		return *inst.PrivateIpAddress
	}
	return ""
}

func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if t.Key != nil && *t.Key == key && t.Value != nil {
			return *t.Value
		}
	}
	return ""
}

func splitRoles(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
