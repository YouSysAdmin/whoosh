package ec2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/yousysadmin/whoosh"
)

type fakeEC2 struct {
	out    *awsec2.DescribeInstancesOutput
	pages  []*awsec2.DescribeInstancesOutput // when set, returned per call in order (paginated response)
	calls  int
	input  *awsec2.DescribeInstancesInput
	inputs []*awsec2.DescribeInstancesInput // every call's input, for asserting NextToken round-trips
}

func (f *fakeEC2) DescribeInstances(_ context.Context, in *awsec2.DescribeInstancesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
	f.input = in
	f.inputs = append(f.inputs, in)
	f.calls++
	if len(f.pages) > 0 {
		return f.pages[f.calls-1], nil
	}
	return f.out, nil
}

type fakeASG struct {
	input         *autoscaling.StartInstanceRefreshInput
	startErr      error                           // returned by StartInstanceRefresh when set
	statuses      []astypes.InstanceRefreshStatus // returned per DescribeInstanceRefreshes call
	describeN     int                             // count of DescribeInstanceRefreshes calls
	emptyDescribe bool                            // DescribeInstanceRefreshes returns no refreshes (vanished)
	groups        []astypes.AutoScalingGroup      // returned by DescribeAutoScalingGroups
}

func (f *fakeASG) StartInstanceRefresh(_ context.Context, in *autoscaling.StartInstanceRefreshInput, _ ...func(*autoscaling.Options)) (*autoscaling.StartInstanceRefreshOutput, error) {
	f.input = in
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &autoscaling.StartInstanceRefreshOutput{InstanceRefreshId: awssdk.String("ir-123")}, nil
}

func (f *fakeASG) DescribeInstanceRefreshes(_ context.Context, _ *autoscaling.DescribeInstanceRefreshesInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeInstanceRefreshesOutput, error) {
	f.describeN++
	if f.emptyDescribe {
		return &autoscaling.DescribeInstanceRefreshesOutput{}, nil
	}
	status := astypes.InstanceRefreshStatusSuccessful
	if len(f.statuses) > 0 {
		idx := f.describeN - 1
		if idx >= len(f.statuses) {
			idx = len(f.statuses) - 1
		}
		status = f.statuses[idx]
	}
	return &autoscaling.DescribeInstanceRefreshesOutput{
		InstanceRefreshes: []astypes.InstanceRefresh{{
			InstanceRefreshId:  awssdk.String("ir-123"),
			Status:             status,
			PercentageComplete: awssdk.Int32(50),
		}},
	}, nil
}

func (f *fakeASG) DescribeAutoScalingGroups(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: f.groups}, nil
}

// fakeLTEC2 fakes the EC2 launch-template calls the rollback action makes.
type fakeLTEC2 struct {
	versions   []int64 // existing launch template version numbers
	newVersion int64   // version number CreateLaunchTemplateVersion returns
	created    *awsec2.CreateLaunchTemplateVersionInput
	modified   *awsec2.ModifyLaunchTemplateInput
}

func (f *fakeLTEC2) DescribeLaunchTemplateVersions(_ context.Context, _ *awsec2.DescribeLaunchTemplateVersionsInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
	var vs []ec2types.LaunchTemplateVersion
	for _, n := range f.versions {
		vs = append(vs, ec2types.LaunchTemplateVersion{VersionNumber: awssdk.Int64(n)})
	}
	return &awsec2.DescribeLaunchTemplateVersionsOutput{LaunchTemplateVersions: vs}, nil
}

func (f *fakeLTEC2) CreateLaunchTemplateVersion(_ context.Context, in *awsec2.CreateLaunchTemplateVersionInput, _ ...func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
	f.created = in
	return &awsec2.CreateLaunchTemplateVersionOutput{
		LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{VersionNumber: awssdk.Int64(f.newVersion)},
	}, nil
}

func (f *fakeLTEC2) ModifyLaunchTemplate(_ context.Context, in *awsec2.ModifyLaunchTemplateInput, _ ...func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error) {
	f.modified = in
	return &awsec2.ModifyLaunchTemplateOutput{}, nil
}

func instance(privateIP string, tags map[string]string) ec2types.Instance {
	var t []ec2types.Tag
	for k, v := range tags {
		t = append(t, ec2types.Tag{Key: awssdk.String(k), Value: awssdk.String(v)})
	}
	return ec2types.Instance{PrivateIpAddress: awssdk.String(privateIP), Tags: t}
}

func TestEC2Inventory_AppendsHostsAndRoles(t *testing.T) {
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{
				instance("10.0.0.1", map[string]string{"roles": "app,web"}),
				instance("10.0.0.2", map[string]string{"roles": "db"}),
			},
		}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{
		Tags:    map[string]stringList{"Environment": {"production"}},
		RoleTag: "roles",
	}}

	cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{{Address: "static1", Roles: []string{"lb"}}}}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}

	// Static host preserved, two discovered appended.
	if len(cfg.Hosts) != 3 {
		t.Fatalf("want 3 hosts, got %d", len(cfg.Hosts))
	}
	if cfg.Hosts[1].Address != "10.0.0.1" || strings.Join(cfg.Hosts[1].Roles, ",") != "app,web" {
		t.Errorf("discovered[0] = %+v", cfg.Hosts[1])
	}
	if cfg.Hosts[2].Address != "10.0.0.2" || strings.Join(cfg.Hosts[2].Roles, ",") != "db" {
		t.Errorf("discovered[1] = %+v", cfg.Hosts[2])
	}

	var names []string
	for _, f := range fe.input.Filters {
		names = append(names, *f.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "instance-state-name,tag:Environment" {
		t.Errorf("unexpected filters: %v", names)
	}
}

func TestEC2Inventory_FallbackRoles(t *testing.T) {
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{instance("10.0.0.9", nil)}}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{Roles: []string{"app"}}}

	cfg := &whoosh.DeployFile{}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	if len(cfg.Hosts) != 1 || strings.Join(cfg.Hosts[0].Roles, ",") != "app" {
		t.Fatalf("fallback roles not applied: %+v", cfg.Hosts)
	}
}

func TestEC2Inventory_TagsScalarOrList(t *testing.T) {
	var p ec2InventoryParams
	if err := whoosh.DecodeParams(map[string]any{
		"tags": map[string]any{
			"Environment": "uat",                // scalar
			"App":         []any{"mb", "pacer"}, // list
		},
	}, &p); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if got := []string(p.Tags["Environment"]); len(got) != 1 || got[0] != "uat" {
		t.Errorf("scalar tag Environment = %v, want [uat]", got)
	}
	if got := []string(p.Tags["App"]); strings.Join(got, ",") != "mb,pacer" {
		t.Errorf("list tag App = %v, want [mb pacer]", got)
	}

	// The values flow into the EC2 filter (multiple = match any).
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{}}
	inv := &ec2Inventory{api: fe, params: p}
	if err := inv.appendHosts(context.Background(), &whoosh.DeployFile{}); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	var appValues []string
	for _, f := range fe.input.Filters {
		if f.Name != nil && *f.Name == "tag:App" {
			appValues = f.Values
		}
	}
	if strings.Join(appValues, ",") != "mb,pacer" {
		t.Errorf("tag:App filter values = %v, want [mb pacer]", appValues)
	}
}

func TestEC2Inventory_DeployTag(t *testing.T) {
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
			instance("10.0.0.1", map[string]string{"Deploy": "yes"}),
			instance("10.0.0.2", map[string]string{"Deploy": "no"}),
			instance("10.0.0.3", nil),
		}}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{
		DeployTag: &tagMatch{Name: "Deploy", Value: "yes"},
	}}

	cfg := &whoosh.DeployFile{}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	if len(cfg.Hosts) != 3 {
		t.Fatalf("want 3 hosts, got %d", len(cfg.Hosts))
	}
	// Only the tag-matching instance deploys; the rest are listed but disabled.
	want := map[string]bool{"10.0.0.1": true, "10.0.0.2": false, "10.0.0.3": false}
	for _, s := range cfg.Hosts {
		if s.Deploy == nil {
			t.Errorf("%s: Deploy is nil, want explicit %v", s.Address, want[s.Address])
			continue
		}
		if *s.Deploy != want[s.Address] {
			t.Errorf("%s: deploy=%v, want %v", s.Address, *s.Deploy, want[s.Address])
		}
	}
}

func TestEC2Inventory_RequiredTag(t *testing.T) {
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
			instance("10.0.0.1", map[string]string{"Role": "db"}),
			instance("10.0.0.2", map[string]string{"Role": "web"}),
		}}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{
		RequiredTag: &tagMatch{Name: "Role", Value: "db"},
	}}

	cfg := &whoosh.DeployFile{}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	want := map[string]bool{"10.0.0.1": true, "10.0.0.2": false}
	for _, s := range cfg.Hosts {
		if s.IsRequired() != want[s.Address] {
			t.Errorf("%s IsRequired = %v, want %v", s.Address, s.IsRequired(), want[s.Address])
		}
	}
}

func TestEC2Inventory_NoDeployTagDefaultsTrue(t *testing.T) {
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{instance("10.0.0.1", nil)}}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{}}

	cfg := &whoosh.DeployFile{}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	if len(cfg.Hosts) != 1 {
		t.Fatalf("want 1 host, got %d", len(cfg.Hosts))
	}
	if cfg.Hosts[0].Deploy != nil {
		t.Errorf("Deploy = %v, want nil (default true) when no deploy_tag set", *cfg.Hosts[0].Deploy)
	}
	if !cfg.Hosts[0].DeployEnabled() {
		t.Errorf("DeployEnabled() = false, want true by default")
	}
}

func TestEC2Inventory_StaticHostTakesPriority(t *testing.T) {
	// A discovered instance whose address is already a static host must be dropped
	// (no duplicate), and the static entry must keep its own deploy flag and roles.
	deployTrue := true
	fe := &fakeEC2{out: &awsec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
			instance("10.0.0.1", map[string]string{"Deploy": "no"}),  // same as the static host below
			instance("10.0.0.2", map[string]string{"Deploy": "yes"}), // genuinely new
		}}},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{
		DeployTag: &tagMatch{Name: "Deploy", Value: "yes"},
	}}

	cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{
		{Address: "10.0.0.1", Roles: []string{"app", "db"}, Deploy: &deployTrue},
	}}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}

	// static 10.0.0.1 + discovered 10.0.0.2, the discovered 10.0.0.1 is skipped.
	if len(cfg.Hosts) != 2 {
		t.Fatalf("want 2 hosts (static + 1 new), got %d: %+v", len(cfg.Hosts), cfg.Hosts)
	}
	var seen int
	for _, h := range cfg.Hosts {
		if h.Address != "10.0.0.1" {
			continue
		}
		seen++
		if !h.DeployEnabled() {
			t.Errorf("static 10.0.0.1 should keep deploy:true, got %v", h.Deploy)
		}
		if strings.Join(h.Roles, ",") != "app,db" {
			t.Errorf("static 10.0.0.1 should keep its roles, got %v", h.Roles)
		}
	}
	if seen != 1 {
		t.Errorf("10.0.0.1 should appear exactly once, got %d", seen)
	}
	// The discovered host is tagged with its inventory feature as the source.
	for _, h := range cfg.Hosts {
		if h.Address == "10.0.0.2" && h.Source != FeatureInventory {
			t.Errorf("discovered host source = %q, want %q", h.Source, FeatureInventory)
		}
	}
}

func TestEC2Inventory_ResolveConfigHosts(t *testing.T) {
	// With resolve_config_hosts, a machine declared by FQDN in the config and discovered by EC2 by IP is deduped:
	// the resolved IPs of every non-IP config address block the discovered entry.
	newFake := func() *fakeEC2 {
		return &fakeEC2{out: &awsec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
				instance("10.0.0.4", nil), // same machine as worker.example.com below
				instance("10.0.0.5", nil), // genuinely new
			}}},
		}}
	}
	lookup := func(_ context.Context, host string) ([]string, error) {
		if host == "worker.example.com" {
			return []string{"10.0.0.4"}, nil
		}
		return nil, fmt.Errorf("no such host %q", host)
	}

	t.Run("resolved duplicate is skipped", func(t *testing.T) {
		inv := &ec2Inventory{api: newFake(), params: ec2InventoryParams{ResolveConfigHosts: true}, lookupHost: lookup}
		cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{{Address: "worker.example.com", Roles: []string{"db"}}}}
		if err := inv.appendHosts(context.Background(), cfg); err != nil {
			t.Fatalf("appendHosts: %v", err)
		}
		if len(cfg.Hosts) != 2 {
			t.Fatalf("want 2 hosts (fqdn + 10.0.0.5), got %d: %+v", len(cfg.Hosts), cfg.Hosts)
		}
		for _, h := range cfg.Hosts {
			if h.Address == "10.0.0.4" {
				t.Errorf("resolved duplicate 10.0.0.4 should be skipped, hosts: %+v", cfg.Hosts)
			}
		}
	})

	t.Run("off by default", func(t *testing.T) {
		inv := &ec2Inventory{api: newFake(), params: ec2InventoryParams{}, lookupHost: lookup}
		cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{{Address: "worker.example.com", Roles: []string{"db"}}}}
		if err := inv.appendHosts(context.Background(), cfg); err != nil {
			t.Fatalf("appendHosts: %v", err)
		}
		if len(cfg.Hosts) != 3 {
			t.Fatalf("without the param both instances append, want 3 hosts, got %d: %+v", len(cfg.Hosts), cfg.Hosts)
		}
	})

	t.Run("lookup failure warns and keeps going", func(t *testing.T) {
		inv := &ec2Inventory{api: newFake(), params: ec2InventoryParams{ResolveConfigHosts: true}, lookupHost: lookup}
		cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{{Address: "gone.example.com", Roles: []string{"db"}}}}
		if err := inv.appendHosts(context.Background(), cfg); err != nil {
			t.Fatalf("appendHosts should not fail on a lookup error: %v", err)
		}
		if len(cfg.Hosts) != 3 {
			t.Fatalf("unresolvable config host dedupes nothing, want 3 hosts, got %d: %+v", len(cfg.Hosts), cfg.Hosts)
		}
	})

	t.Run("ip literals are not resolved", func(t *testing.T) {
		called := false
		inv := &ec2Inventory{api: newFake(), params: ec2InventoryParams{ResolveConfigHosts: true},
			lookupHost: func(ctx context.Context, host string) ([]string, error) {
				called = true
				return lookup(ctx, host)
			}}
		cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{{Address: "10.0.0.4", Roles: []string{"db"}}}}
		if err := inv.appendHosts(context.Background(), cfg); err != nil {
			t.Fatalf("appendHosts: %v", err)
		}
		if called {
			t.Error("an IP-literal address must not be resolved")
		}
	})
}

func TestEC2Inventory_Paginates(t *testing.T) {
	// DescribeInstances is paginated; instances on later pages must be appended too,
	// with the NextToken from each page passed to the next call.
	fe := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{
		{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{instance("10.0.0.1", nil)}}},
			NextToken:    awssdk.String("page2"),
		},
		{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{instance("10.0.0.2", nil)}}},
		},
	}}
	inv := &ec2Inventory{api: fe, params: ec2InventoryParams{Roles: []string{"app"}}}

	cfg := &whoosh.DeployFile{}
	if err := inv.appendHosts(context.Background(), cfg); err != nil {
		t.Fatalf("appendHosts: %v", err)
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("want 2 hosts across pages, got %d: %+v", len(cfg.Hosts), cfg.Hosts)
	}
	if cfg.Hosts[0].Address != "10.0.0.1" || cfg.Hosts[1].Address != "10.0.0.2" {
		t.Errorf("hosts = %+v, want 10.0.0.1 then 10.0.0.2", cfg.Hosts)
	}
	if fe.calls != 2 {
		t.Fatalf("want 2 DescribeInstances calls, got %d", fe.calls)
	}
	if fe.inputs[0].NextToken != nil {
		t.Errorf("first call NextToken = %v, want nil", *fe.inputs[0].NextToken)
	}
	if fe.inputs[1].NextToken == nil || *fe.inputs[1].NextToken != "page2" {
		t.Errorf("second call NextToken = %v, want page2", fe.inputs[1].NextToken)
	}
}

func TestASGRefresh_StartsRefresh(t *testing.T) {
	fa := &fakeASG{}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}

	// Action narrative goes through slog, capture the default logger.
	var logbuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logbuf, nil)))
	defer slog.SetDefault(prev)

	if err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if fa.input == nil || fa.input.AutoScalingGroupName == nil || *fa.input.AutoScalingGroupName != "my-asg" {
		t.Fatalf("ASG name not passed: %+v", fa.input)
	}

	// Full preference set is sent with the gem defaults when nothing is overridden.
	p := fa.input.Preferences
	if p == nil {
		t.Fatal("Preferences not set")
	}
	if awssdk.ToInt32(p.MinHealthyPercentage) != 100 || awssdk.ToInt32(p.MaxHealthyPercentage) != 200 ||
		awssdk.ToInt32(p.InstanceWarmup) != 300 || !awssdk.ToBool(p.SkipMatching) || awssdk.ToBool(p.AutoRollback) {
		t.Errorf("default preferences wrong: min=%v max=%v warmup=%v skip=%v rollback=%v",
			awssdk.ToInt32(p.MinHealthyPercentage), awssdk.ToInt32(p.MaxHealthyPercentage),
			awssdk.ToInt32(p.InstanceWarmup), awssdk.ToBool(p.SkipMatching), awssdk.ToBool(p.AutoRollback))
	}
	if !strings.Contains(logbuf.String(), "ir-123") {
		t.Fatalf("missing refresh id in slog output: %q", logbuf.String())
	}
	if !strings.Contains(logbuf.String(), "instance refresh complete") {
		t.Fatalf("expected completion log after polling:\n%s", logbuf.String())
	}
}

func TestASGRefresh_OverridesPreferences(t *testing.T) {
	fa := &fakeASG{}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}
	err := a.runRefresh(context.Background(), map[string]any{
		"name":                   "my-asg",
		"min_healthy_percentage": 90,
		"max_healthy_percentage": 150,
		"instance_warmup":        120,
		"skip_matching":          false,
		"auto_rollback":          true,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	p := fa.input.Preferences
	if awssdk.ToInt32(p.MinHealthyPercentage) != 90 || awssdk.ToInt32(p.MaxHealthyPercentage) != 150 ||
		awssdk.ToInt32(p.InstanceWarmup) != 120 || awssdk.ToBool(p.SkipMatching) || !awssdk.ToBool(p.AutoRollback) {
		t.Errorf("overrides not applied: %+v", p)
	}
}

func TestASGRefresh_PollsUntilComplete(t *testing.T) {
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{
		astypes.InstanceRefreshStatusPending,
		astypes.InstanceRefreshStatusInProgress,
		astypes.InstanceRefreshStatusSuccessful,
	}}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}

	var logbuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logbuf, nil)))
	defer slog.SetDefault(prev)

	if err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v\n%s", err, logbuf.String())
	}
	if fa.describeN != 3 {
		t.Errorf("polled %d times, want 3 (pending, in-progress, successful)", fa.describeN)
	}
	if !strings.Contains(logbuf.String(), "status=InProgress") {
		t.Errorf("expected periodic in-progress status in log:\n%s", logbuf.String())
	}
}

func TestASGRefresh_FailsOnFailedStatus(t *testing.T) {
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusFailed}}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}
	if err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when the refresh ends in a Failed state")
	}
}

func TestASGRefresh_SkipsWhenAlreadyInProgress(t *testing.T) {
	fa := &fakeASG{startErr: &astypes.InstanceRefreshInProgressFault{}}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}
	if err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("an already-running refresh should be skipped, not fail: %v", err)
	}
	if fa.describeN != 0 {
		t.Errorf("should not poll when the refresh was not started, polled %d times", fa.describeN)
	}
}

func TestASGRefresh_RequiresName(t *testing.T) {
	a := &asgPlugin{api: &fakeASG{}}
	if err := a.runRefresh(context.Background(), map[string]any{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when ASG name is missing")
	}
}

func TestASGRefresh_FailsOnCancelledStatus(t *testing.T) {
	// The refresh was cancelled out-of-band (e.g. in the console) - the wait must end with an error, not hang.
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusCancelled}}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}
	err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when the refresh ends in a Cancelled state")
	}
	if !strings.Contains(err.Error(), "Cancelled") {
		t.Errorf("error = %v, want it to report the Cancelled status", err)
	}
}

func TestASGRefresh_FailsWhenRefreshDisappears(t *testing.T) {
	// DescribeInstanceRefreshes returns nothing for the id - the refresh vanished.
	fa := &fakeASG{emptyDescribe: true}
	a := &asgPlugin{api: fa, pollInterval: time.Millisecond}
	err := a.runRefresh(context.Background(), map[string]any{"name": "my-asg"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when the refresh can't be found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want a 'not found' message", err)
	}
}

func TestASGRefresh_CancelStopsWaiting(t *testing.T) {
	// In-progress + a cancelled context: the wait stops and reports the cancel, without hanging (a long poll interval
	// would block forever otherwise).
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusInProgress}}
	a := &asgPlugin{api: fa, pollInterval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := a.waitForRefresh(ctx, "my-asg", "ir-123")
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "continues in AWS") {
		t.Errorf("error = %v, want it to note the refresh keeps running in AWS", err)
	}
}

func TestASGRollback_CopiesPreviousVersionThenRefreshes(t *testing.T) {
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusSuccessful}}
	fe := &fakeLTEC2{versions: []int64{1, 2, 3}, newVersion: 4}
	a := &asgPlugin{api: fa, ec2: fe, pollInterval: time.Millisecond}

	err := a.runRollback(context.Background(), map[string]any{
		"name":            "web-asg",
		"launch_template": map[string]any{"id": "lt-1"},
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("runRollback: %v", err)
	}

	// The new version is copied from the previous (second-highest = 2).
	if fe.created == nil || awssdk.ToString(fe.created.SourceVersion) != "2" {
		t.Errorf("SourceVersion = %v, want 2 (previous of latest 3)", fe.created)
	}
	if awssdk.ToString(fe.created.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", awssdk.ToString(fe.created.LaunchTemplateId))
	}
	// By default the copied (new latest) version becomes the default.
	if fe.modified == nil || awssdk.ToString(fe.modified.DefaultVersion) != "4" {
		t.Errorf("default version = %v, want 4 (the new copy)", fe.modified)
	}
	// Then a refresh is started onto it.
	if fa.input == nil || awssdk.ToString(fa.input.AutoScalingGroupName) != "web-asg" {
		t.Errorf("refresh not started for web-asg: %+v", fa.input)
	}
}

func TestASGRollback_ResolvesLaunchTemplateFromASG(t *testing.T) {
	fa := &fakeASG{
		statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusSuccessful},
		groups: []astypes.AutoScalingGroup{{
			LaunchTemplate: &astypes.LaunchTemplateSpecification{LaunchTemplateId: awssdk.String("lt-from-asg")},
		}},
	}
	fe := &fakeLTEC2{versions: []int64{5, 7}, newVersion: 8} // non-contiguous, previous = 5
	a := &asgPlugin{api: fa, ec2: fe, pollInterval: time.Millisecond}

	if err := a.runRollback(context.Background(), map[string]any{"name": "web-asg"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runRollback: %v", err)
	}
	if awssdk.ToString(fe.created.LaunchTemplateId) != "lt-from-asg" {
		t.Errorf("LT not resolved from ASG: %q", awssdk.ToString(fe.created.LaunchTemplateId))
	}
	if awssdk.ToString(fe.created.SourceVersion) != "5" {
		t.Errorf("SourceVersion = %q, want 5 (second-highest of [5,7])", awssdk.ToString(fe.created.SourceVersion))
	}
}

func TestASGRollback_SetDefaultFalseSkipsModify(t *testing.T) {
	fa := &fakeASG{statuses: []astypes.InstanceRefreshStatus{astypes.InstanceRefreshStatusSuccessful}}
	fe := &fakeLTEC2{versions: []int64{1, 2}, newVersion: 3}
	a := &asgPlugin{api: fa, ec2: fe, pollInterval: time.Millisecond}

	err := a.runRollback(context.Background(), map[string]any{
		"name":            "web-asg",
		"launch_template": map[string]any{"id": "lt-1"},
		"set_default":     false,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("runRollback: %v", err)
	}
	if fe.modified != nil {
		t.Errorf("set_default: false should not modify the default version, got %+v", fe.modified)
	}
}

func TestASGRollback_FailsWithSingleVersion(t *testing.T) {
	fe := &fakeLTEC2{versions: []int64{1}, newVersion: 2}
	a := &asgPlugin{api: &fakeASG{}, ec2: fe, pollInterval: time.Millisecond}

	err := a.runRollback(context.Background(), map[string]any{
		"name":            "web-asg",
		"launch_template": map[string]any{"id": "lt-1"},
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error rolling back a launch template with only one version")
	}
	if fe.created != nil {
		t.Error("must not create a version when there is no previous one")
	}
}

func TestASGRollback_RequiresName(t *testing.T) {
	a := &asgPlugin{api: &fakeASG{}, ec2: &fakeLTEC2{}}
	if err := a.runRollback(context.Background(), map[string]any{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when ASG name is missing")
	}
}
