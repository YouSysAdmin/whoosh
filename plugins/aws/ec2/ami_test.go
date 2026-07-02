package ec2

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// fakeAMIEC2 implements amiEC2API, serving canned describe data and recording the mutating calls (create image,
// launch-template patch, deregister/delete).
type fakeAMIEC2 struct {
	instances    []ec2types.Instance // returned by DescribeInstances
	images       []ec2types.Image    // returned by DescribeImages (when imagesByCall is empty)
	imagesByCall [][]ec2types.Image  // returned per DescribeImages call, in order
	imagesCalls  int                 // count of DescribeImages calls
	describeErr  error               // when set, DescribeImages returns it
	ltVersion    int64               // version returned by CreateLaunchTemplateVersion

	createImage    *awsec2.CreateImageInput
	createLTV      *awsec2.CreateLaunchTemplateVersionInput
	modifyLT       *awsec2.ModifyLaunchTemplateInput
	describeImages *awsec2.DescribeImagesInput
	deregistered   []string
	deletedSnaps   []string
}

func (f *fakeAMIEC2) DescribeInstances(_ context.Context, _ *awsec2.DescribeInstancesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
	return &awsec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: f.instances}}}, nil
}

func (f *fakeAMIEC2) CreateImage(_ context.Context, in *awsec2.CreateImageInput, _ ...func(*awsec2.Options)) (*awsec2.CreateImageOutput, error) {
	f.createImage = in
	return &awsec2.CreateImageOutput{ImageId: awssdk.String("ami-new")}, nil
}

func (f *fakeAMIEC2) DescribeImages(_ context.Context, in *awsec2.DescribeImagesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeImagesOutput, error) {
	f.describeImages = in
	f.imagesCalls++
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if len(f.imagesByCall) > 0 {
		idx := f.imagesCalls - 1
		if idx >= len(f.imagesByCall) {
			idx = len(f.imagesByCall) - 1
		}
		return &awsec2.DescribeImagesOutput{Images: f.imagesByCall[idx]}, nil
	}
	return &awsec2.DescribeImagesOutput{Images: f.images}, nil
}

func (f *fakeAMIEC2) DeregisterImage(_ context.Context, in *awsec2.DeregisterImageInput, _ ...func(*awsec2.Options)) (*awsec2.DeregisterImageOutput, error) {
	f.deregistered = append(f.deregistered, awssdk.ToString(in.ImageId))
	return &awsec2.DeregisterImageOutput{}, nil
}

func (f *fakeAMIEC2) DeleteSnapshot(_ context.Context, in *awsec2.DeleteSnapshotInput, _ ...func(*awsec2.Options)) (*awsec2.DeleteSnapshotOutput, error) {
	f.deletedSnaps = append(f.deletedSnaps, awssdk.ToString(in.SnapshotId))
	return &awsec2.DeleteSnapshotOutput{}, nil
}

func (f *fakeAMIEC2) CreateLaunchTemplateVersion(_ context.Context, in *awsec2.CreateLaunchTemplateVersionInput, _ ...func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
	f.createLTV = in
	return &awsec2.CreateLaunchTemplateVersionOutput{
		LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{VersionNumber: awssdk.Int64(f.ltVersion)},
	}, nil
}

func (f *fakeAMIEC2) ModifyLaunchTemplate(_ context.Context, in *awsec2.ModifyLaunchTemplateInput, _ ...func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error) {
	f.modifyLT = in
	return &awsec2.ModifyLaunchTemplateOutput{}, nil
}

type fakeAMIASG struct {
	group astypes.AutoScalingGroup
}

func (f *fakeAMIASG) DescribeAutoScalingGroups(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{f.group}}, nil
}

// availableImage is the canned image the create waiter sees (state=available, so ImageAvailableWaiter returns on its
// first poll).
func availableImage() ec2types.Image {
	return ec2types.Image{ImageId: awssdk.String("ami-new"), State: ec2types.ImageStateAvailable}
}

func imageTags(spec []ec2types.TagSpecification) map[string]string {
	out := map[string]string{}
	for _, ts := range spec {
		if ts.ResourceType != ec2types.ResourceTypeImage {
			continue
		}
		for _, t := range ts.Tags {
			out[awssdk.ToString(t.Key)] = awssdk.ToString(t.Value)
		}
	}
	return out
}

func TestAMICreate_FromTagsAndPatchesLaunchTemplate(t *testing.T) {
	src := instance("10.0.0.5", map[string]string{
		"Name":                      "web-1",
		"Application":               "managebac",
		"aws:autoscaling:groupName": "managebac-asg", // aws: tag must be dropped
	})
	src.InstanceId = awssdk.String("i-source")

	fe := &fakeAMIEC2{instances: []ec2types.Instance{src}, images: []ec2types.Image{availableImage()}, ltVersion: 7}
	fa := &fakeAMIASG{group: astypes.AutoScalingGroup{
		LaunchTemplate: &astypes.LaunchTemplateSpecification{LaunchTemplateId: awssdk.String("lt-123")},
	}}
	p := &amiPlugin{ec2: fe, asg: fa}

	var buf bytes.Buffer
	err := p.runCreate(context.Background(), map[string]any{
		"name_prefix":     "managebac",
		"source_tags":     map[string]any{"Role": "web"},
		"launch_template": map[string]any{"asg": "managebac-asg"},
	}, &buf)
	if err != nil {
		t.Fatalf("runCreate: %v\n%s", err, buf.String())
	}

	// CreateImage: NoReboot defaults true; name carries the prefix.
	if fe.createImage == nil {
		t.Fatal("CreateImage not called")
	}
	if fe.createImage.NoReboot == nil || !*fe.createImage.NoReboot {
		t.Errorf("NoReboot = %v, want true (default)", fe.createImage.NoReboot)
	}
	name := awssdk.ToString(fe.createImage.Name)
	if !strings.HasPrefix(name, "managebac-") {
		t.Errorf("AMI name = %q, want managebac-<timestamp>", name)
	}

	// Tags: copy app tags, drop Name + aws:*, set Name to the AMI name.
	tags := imageTags(fe.createImage.TagSpecifications)
	if tags["Application"] != "managebac" {
		t.Errorf("Application tag = %q, want managebac", tags["Application"])
	}
	if tags["Name"] != name {
		t.Errorf("Name tag = %q, want %q", tags["Name"], name)
	}
	if _, ok := tags["aws:autoscaling:groupName"]; ok {
		t.Error("aws: tag should be dropped from the AMI")
	}

	// Launch template patched to the new AMI and made default.
	if fe.createLTV == nil || awssdk.ToString(fe.createLTV.LaunchTemplateId) != "lt-123" {
		t.Fatalf("CreateLaunchTemplateVersion not called for lt-123: %+v", fe.createLTV)
	}
	if awssdk.ToString(fe.createLTV.SourceVersion) != "$Default" {
		t.Errorf("SourceVersion = %q, want $Default", awssdk.ToString(fe.createLTV.SourceVersion))
	}
	if fe.createLTV.LaunchTemplateData == nil || awssdk.ToString(fe.createLTV.LaunchTemplateData.ImageId) != "ami-new" {
		t.Errorf("new LT version image = %+v, want ami-new", fe.createLTV.LaunchTemplateData)
	}
	if fe.modifyLT == nil || awssdk.ToString(fe.modifyLT.DefaultVersion) != "7" {
		t.Fatalf("ModifyLaunchTemplate default = %+v, want version 7", fe.modifyLT)
	}
}

func TestAMICreate_FromASGInServiceInstanceNamePrefixFallback(t *testing.T) {
	src := instance("10.0.0.6", map[string]string{"Name": "worker-1"})
	src.InstanceId = awssdk.String("i-worker")

	fe := &fakeAMIEC2{instances: []ec2types.Instance{src}, images: []ec2types.Image{availableImage()}}
	fa := &fakeAMIASG{group: astypes.AutoScalingGroup{
		Instances: []astypes.Instance{
			{InstanceId: awssdk.String("i-pending"), LifecycleState: astypes.LifecycleStatePending},
			{InstanceId: awssdk.String("i-worker"), LifecycleState: astypes.LifecycleStateInService},
		},
	}}
	p := &amiPlugin{ec2: fe, asg: fa}

	var buf bytes.Buffer
	if err := p.runCreate(context.Background(), map[string]any{"asg": "workers"}, &buf); err != nil {
		t.Fatalf("runCreate: %v\n%s", err, buf.String())
	}
	// No name_prefix -> falls back to the source instance's Name tag.
	if name := awssdk.ToString(fe.createImage.Name); !strings.HasPrefix(name, "worker-1-") {
		t.Errorf("AMI name = %q, want worker-1-<timestamp>", name)
	}
	// No launch_template -> no patch.
	if fe.createLTV != nil {
		t.Error("launch template should not be patched when not requested")
	}
}

func TestAMICreate_FromExplicitInstanceID(t *testing.T) {
	src := instance("10.0.0.7", map[string]string{"Name": "chosen-1"})
	src.InstanceId = awssdk.String("i-explicit")

	// No source_tags/asg: instance_id names the source directly. The ASG client is nil to prove it is never consulted.
	fe := &fakeAMIEC2{instances: []ec2types.Instance{src}, images: []ec2types.Image{availableImage()}}
	p := &amiPlugin{ec2: fe, asg: nil}

	var buf bytes.Buffer
	if err := p.runCreate(context.Background(), map[string]any{"instance_id": "i-explicit"}, &buf); err != nil {
		t.Fatalf("runCreate: %v\n%s", err, buf.String())
	}
	if name := awssdk.ToString(fe.createImage.Name); !strings.HasPrefix(name, "chosen-1-") {
		t.Errorf("AMI name = %q, want chosen-1-<timestamp>", name)
	}
}

func TestAMICreate_PollsImageStateUntilAvailable(t *testing.T) {
	src := instance("10.0.0.8", map[string]string{"Name": "poll-1"})
	src.InstanceId = awssdk.String("i-poll")

	pending := []ec2types.Image{{ImageId: awssdk.String("ami-new"), State: ec2types.ImageStatePending}}
	fe := &fakeAMIEC2{
		instances:    []ec2types.Instance{src},
		imagesByCall: [][]ec2types.Image{pending, pending, {availableImage()}},
	}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}, pollInterval: time.Millisecond}

	// Action narrative goes through slog; capture it to assert progress is logged.
	var logbuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logbuf, nil)))
	defer slog.SetDefault(prev)

	if err := p.runCreate(context.Background(), map[string]any{"instance_id": "i-poll", "name_prefix": "poll"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runCreate: %v\n%s", err, logbuf.String())
	}
	if fe.imagesCalls != 3 {
		t.Errorf("polled DescribeImages %d times, want 3 (pending, pending, available)", fe.imagesCalls)
	}
	if !strings.Contains(logbuf.String(), "state=pending") {
		t.Errorf("expected periodic pending-state progress in log:\n%s", logbuf.String())
	}
	if !strings.Contains(logbuf.String(), "AMI is available") {
		t.Errorf("expected final available log:\n%s", logbuf.String())
	}
}

func TestAMICreate_FailsOnImageFailureState(t *testing.T) {
	src := instance("10.0.0.9", map[string]string{"Name": "bad-1"})
	src.InstanceId = awssdk.String("i-bad")
	fe := &fakeAMIEC2{
		instances: []ec2types.Instance{src},
		images:    []ec2types.Image{{ImageId: awssdk.String("ami-new"), State: ec2types.ImageStateFailed}},
	}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}, pollInterval: time.Millisecond}
	if err := p.runCreate(context.Background(), map[string]any{"instance_id": "i-bad", "name_prefix": "bad"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when the image enters a failed state")
	}
}

func TestAMICreate_FailsWhenImageDeletedMidBuild(t *testing.T) {
	src := instance("10.0.0.5", map[string]string{"Name": "gone-1"})
	src.InstanceId = awssdk.String("i-gone")
	pending := []ec2types.Image{{ImageId: awssdk.String("ami-new"), State: ec2types.ImageStatePending}}
	fe := &fakeAMIEC2{
		instances:    []ec2types.Instance{src},
		imagesByCall: [][]ec2types.Image{pending, {}}, // seen pending, then it vanishes
	}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}, pollInterval: time.Millisecond}

	err := p.runCreate(context.Background(), map[string]any{"instance_id": "i-gone", "name_prefix": "gone"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when the AMI is deleted mid-build, not an endless wait")
	}
	if !strings.Contains(err.Error(), "disappeared") {
		t.Errorf("error = %v, want it to report the image disappeared", err)
	}
}

func TestAMICreate_FailsWhenImageNotFound(t *testing.T) {
	src := instance("10.0.0.6", map[string]string{"Name": "nf-1"})
	src.InstanceId = awssdk.String("i-nf")
	fe := &fakeAMIEC2{
		instances:   []ec2types.Instance{src},
		describeErr: &smithy.GenericAPIError{Code: "InvalidAMIID.NotFound", Message: "does not exist"},
	}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}, pollInterval: time.Millisecond}

	err := p.runCreate(context.Background(), map[string]any{"instance_id": "i-nf", "name_prefix": "nf"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when DescribeImages returns InvalidAMIID.NotFound")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("error = %v, want a clear 'no longer exists' message", err)
	}
}

func TestWaitForImage_CancelReportsCancellation(t *testing.T) {
	fe := &fakeAMIEC2{images: []ec2types.Image{{ImageId: awssdk.String("ami-x"), State: ec2types.ImageStatePending}}}
	p := &amiPlugin{ec2: fe, pollInterval: time.Hour} // long interval: it blocks until ctx ends
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate Ctrl-C before the next poll

	err := p.waitForImage(ctx, "ami-x")
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	// The message must reflect the cancel, NOT claim the 30-minute timeout elapsed.
	if !strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), amiMaxWait.String()) {
		t.Errorf("error = %v, want a cancellation message (not the %s timeout)", err, amiMaxWait)
	}
}

func TestAMICreate_RequiresSource(t *testing.T) {
	p := &amiPlugin{ec2: &fakeAMIEC2{}, asg: &fakeAMIASG{}}
	if err := p.runCreate(context.Background(), map[string]any{"name_prefix": "x"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when neither source_tags nor asg is set")
	}
}

func TestAMICleanup_KeepsNewestAndDeletesSnapshots(t *testing.T) {
	img := func(id, name, date, snap string) ec2types.Image {
		return ec2types.Image{
			ImageId:      awssdk.String(id),
			Name:         awssdk.String(name),
			CreationDate: awssdk.String(date),
			BlockDeviceMappings: []ec2types.BlockDeviceMapping{
				{Ebs: &ec2types.EbsBlockDevice{SnapshotId: awssdk.String(snap)}},
			},
		}
	}
	fe := &fakeAMIEC2{images: []ec2types.Image{
		img("ami-1", "app-2026-01-01", "2026-01-01T00:00:00.000Z", "snap-1"),
		img("ami-2", "app-2026-02-01", "2026-02-01T00:00:00.000Z", "snap-2"),
		img("ami-3", "app-2026-03-01", "2026-03-01T00:00:00.000Z", "snap-3"),
		img("ami-4", "app-2026-04-01", "2026-04-01T00:00:00.000Z", "snap-4"),
		img("ami-x", "other-2026-09-09", "2026-09-09T00:00:00.000Z", "snap-x"), // different prefix; ignored
	}}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}}

	var buf bytes.Buffer
	if err := p.runCleanup(context.Background(), map[string]any{"name_prefix": "app", "keep_last": 2}, &buf); err != nil {
		t.Fatalf("runCleanup: %v\n%s", err, buf.String())
	}
	// Keep the 2 newest matching (ami-4, ami-3); remove the 2 oldest (ami-2, ami-1).
	if strings.Join(fe.deregistered, ",") != "ami-2,ami-1" {
		t.Errorf("deregistered = %v, want [ami-2 ami-1]", fe.deregistered)
	}
	if strings.Join(fe.deletedSnaps, ",") != "snap-2,snap-1" {
		t.Errorf("deleted snapshots = %v, want [snap-2 snap-1]", fe.deletedSnaps)
	}
	// The non-matching image is untouched.
	for _, id := range fe.deregistered {
		if id == "ami-x" {
			t.Error("ami-x has a different prefix and must not be removed")
		}
	}
}

func TestAMICleanup_FiltersByTags(t *testing.T) {
	mk := func(id, name, date string) ec2types.Image {
		return ec2types.Image{ImageId: awssdk.String(id), Name: awssdk.String(name), CreationDate: awssdk.String(date)}
	}
	fe := &fakeAMIEC2{images: []ec2types.Image{
		mk("ami-1", "api-2026-01-01", "2026-01-01T00:00:00Z"),
		mk("ami-2", "api-2026-02-01", "2026-02-01T00:00:00Z"),
		mk("ami-3", "api-2026-03-01", "2026-03-01T00:00:00Z"),
		mk("ami-x", "other-2026-09-09", "2026-09-09T00:00:00Z"), // wrong name prefix, excluded client-side
	}}
	p := &amiPlugin{ec2: fe, asg: &fakeAMIASG{}}

	var buf bytes.Buffer
	err := p.runCleanup(context.Background(), map[string]any{
		"name_prefix": "api",
		"tags":        map[string]any{"Application": "api", "Environment": []any{"uat", "prod"}},
		"keep_last":   1,
	}, &buf)
	if err != nil {
		t.Fatalf("runCleanup: %v\n%s", err, buf.String())
	}

	// Tag filters are pushed to DescribeImages (server-side), alongside owners=self.
	if fe.describeImages == nil {
		t.Fatal("DescribeImages not called")
	}
	if strings.Join(fe.describeImages.Owners, ",") != "self" {
		t.Errorf("Owners = %v, want [self]", fe.describeImages.Owners)
	}
	got := map[string]string{}
	for _, f := range fe.describeImages.Filters {
		got[awssdk.ToString(f.Name)] = strings.Join(f.Values, ",")
	}
	if got["tag:Application"] != "api" {
		t.Errorf("tag:Application filter = %q, want api", got["tag:Application"])
	}
	if got["tag:Environment"] != "uat,prod" {
		t.Errorf("tag:Environment filter = %q, want uat,prod", got["tag:Environment"])
	}

	// name_prefix still applies client-side: keep newest api- (ami-3), prune ami-2 then ami-1; the other-prefixed image is
	// excluded entirely.
	if strings.Join(fe.deregistered, ",") != "ami-2,ami-1" {
		t.Errorf("deregistered = %v, want [ami-2 ami-1]", fe.deregistered)
	}
}

func TestAMICleanup_RequiresFilter(t *testing.T) {
	p := &amiPlugin{ec2: &fakeAMIEC2{}, asg: &fakeAMIASG{}}
	if err := p.runCleanup(context.Background(), map[string]any{"keep_last": 3}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error when neither name_prefix nor tags is set")
	}
}
