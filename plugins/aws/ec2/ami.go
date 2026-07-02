package ec2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	awsparams "github.com/yousysadmin/whoosh/plugins/aws/internal/params"
)

// Action names, namespaced under the plugins ("<plugins>:<action>").
const (
	actionAMICreate  = FeatureAMI + ":create"
	actionAMICleanup = FeatureAMI + ":cleanup"
)

const (
	// amiMaxWait bounds the total wait for a new image to become available.
	amiMaxWait = 30 * time.Minute
	// amiPollInterval is how often aws:ec2:ami:create polls (and logs) image state while waiting, so a long bake isn't
	// mistaken for a hang.
	amiPollInterval = 30 * time.Second
)

// amiEC2API is the slice of the EC2 client the AMI actions use.
type amiEC2API interface {
	DescribeInstances(context.Context, *awsec2.DescribeInstancesInput, ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error)
	CreateImage(context.Context, *awsec2.CreateImageInput, ...func(*awsec2.Options)) (*awsec2.CreateImageOutput, error)
	DescribeImages(context.Context, *awsec2.DescribeImagesInput, ...func(*awsec2.Options)) (*awsec2.DescribeImagesOutput, error)
	DeregisterImage(context.Context, *awsec2.DeregisterImageInput, ...func(*awsec2.Options)) (*awsec2.DeregisterImageOutput, error)
	DeleteSnapshot(context.Context, *awsec2.DeleteSnapshotInput, ...func(*awsec2.Options)) (*awsec2.DeleteSnapshotOutput, error)
	CreateLaunchTemplateVersion(context.Context, *awsec2.CreateLaunchTemplateVersionInput, ...func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error)
	ModifyLaunchTemplate(context.Context, *awsec2.ModifyLaunchTemplateInput, ...func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error)
}

// amiASGAPI is the slice of the Auto Scaling client the AMI actions use - to pick a source instance from a group and to
// find the launch template attached to it.
type amiASGAPI interface {
	DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

// amiPlugin registers the aws:ec2:ami:create and aws:ec2:ami:cleanup actions: build an image from a running instance,
// point a launch template at it, and prune old images.
type amiPlugin struct {
	ec2 amiEC2API
	asg amiASGAPI
	// defaults are the feature-level params (the `aws:ec2:ami` actions: entry), layered under each task's `with:` (the
	// task wins).
	defaults map[string]any
	// pollInterval is how often runCreate polls image state while waiting, zero means amiPollInterval.
	// Overridden in tests to avoid real sleeps.
	pollInterval time.Duration
}

// amiLTTarget selects the launch template aws:ec2:ami:create patches after the new AMI is available: an explicit
// template id, or the template attached to an ASG.
type amiLTTarget struct {
	ID  string `yaml:"id"`
	ASG string `yaml:"asg"`
}

// amiCreateParams are the aws:ec2:ami:create action params (a task's `with:`).
type amiCreateParams struct {
	// NamePrefix is the AMI Name prefix, the image is named "<prefix>-<timestamp>".
	// When empty it falls back to the source instance's Name tag.
	NamePrefix string `yaml:"name_prefix"`
	// Source instance, in precedence order (set one): InstanceID names it directly, SourceTags selects the first running
	// instance matching every tag, ASG selects the first InService instance in the group.
	InstanceID string            `yaml:"instance_id"`
	SourceTags map[string]string `yaml:"source_tags"`
	ASG        string            `yaml:"asg"`
	// NoReboot defaults to true (snapshot without stopping the instance).
	NoReboot *bool `yaml:"no_reboot"`
	// Wait defaults to true: block until the image is available.
	// Forced on when LaunchTemplate is set (a launch template must not point at a pending image).
	Wait *bool `yaml:"wait"`
	// LaunchTemplate, when set, is patched to the new AMI once it is available.
	LaunchTemplate *amiLTTarget `yaml:"launch_template"`
}

// amiCleanupParams are the aws:ec2:ami:cleanup action params.
// NamePrefix and Tags narrow which self-owned images are pruning candidates, set at least one (both are AND-ed).
// Each tag value is a scalar or a list (matches any of the values).
type amiCleanupParams struct {
	NamePrefix string                `yaml:"name_prefix"`
	Tags       map[string]stringList `yaml:"tags"`
	KeepLast   int                   `yaml:"keep_last"`
}

// runCreate builds an AMI from a chosen source instance, optionally waits for it to become available, and optionally
// points a launch template at it. Progress is whoosh narrative, so it goes through slog (not the action's out writer).
func (p *amiPlugin) runCreate(ctx context.Context, params map[string]any, _ io.Writer) error {
	var ap amiCreateParams
	if err := awsparams.DecodeFeature(p.defaults, params, &ap); err != nil {
		return err
	}
	if ap.InstanceID == "" && len(ap.SourceTags) == 0 && ap.ASG == "" {
		return fmt.Errorf("%s: set 'instance_id', 'source_tags', or 'asg' to choose the source instance", actionAMICreate)
	}

	instanceID, err := p.sourceInstanceID(ctx, ap)
	if err != nil {
		return fmt.Errorf("%s: %w", actionAMICreate, err)
	}
	inst, err := p.describeInstance(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("%s: %w", actionAMICreate, err)
	}

	base := ap.NamePrefix
	if base == "" {
		base = tagValue(inst.Tags, "Name")
	}
	if base == "" {
		return fmt.Errorf("%s: set 'name_prefix' (source instance %s has no Name tag)", actionAMICreate, instanceID)
	}
	amiName := base + "-" + time.Now().UTC().Format("20060102-150405")
	noReboot := ap.NoReboot == nil || *ap.NoReboot

	slog.Info("creating AMI", "name", amiName, "instance", instanceID)
	tags := amiTags(inst.Tags, amiName)
	img, err := p.ec2.CreateImage(ctx, &awsec2.CreateImageInput{
		InstanceId: awssdk.String(instanceID),
		Name:       awssdk.String(amiName),
		NoReboot:   awssdk.Bool(noReboot),
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeImage, Tags: tags},
			{ResourceType: ec2types.ResourceTypeSnapshot, Tags: tags},
		},
	})
	if err != nil {
		return fmt.Errorf("%s: create image: %w", actionAMICreate, err)
	}
	amiID := awssdk.ToString(img.ImageId)
	slog.Info("created AMI", "ami", amiID)

	// A launch template must never point at a pending image, so patching forces the wait regardless of the `wait` flag.
	wait := ap.Wait == nil || *ap.Wait || ap.LaunchTemplate != nil
	if wait {
		if err := p.waitForImage(ctx, amiID); err != nil {
			return fmt.Errorf("%s: %w", actionAMICreate, err)
		}
	}

	if ap.LaunchTemplate != nil {
		if err := p.patchLaunchTemplate(ctx, *ap.LaunchTemplate, amiID); err != nil {
			return fmt.Errorf("%s: %w", actionAMICreate, err)
		}
	}
	return nil
}

// waitForImage polls the new image until it is available, logging its state each interval so a long bake isn't mistaken
// for a hang. A failure state aborts immediately, the whole wait is bounded by amiMaxWait.
func (p *amiPlugin) waitForImage(ctx context.Context, amiID string) error {
	interval := p.pollInterval
	if interval <= 0 {
		interval = amiPollInterval
	}
	ctx, cancel := context.WithTimeout(ctx, amiMaxWait)
	defer cancel()

	seen := false // whether DescribeImages has ever returned the image
	for attempt := 1; ; attempt++ {
		resp, err := p.ec2.DescribeImages(ctx, &awsec2.DescribeImagesInput{ImageIds: []string{amiID}})
		if err != nil {
			// A deregistered/deleted image comes back as InvalidAMIID.NotFound - stop with an actionable message instead of
			// polling to the timeout.
			if imageNotFound(err) {
				return fmt.Errorf("AMI %s no longer exists (deregistered or deleted while pending?)", amiID)
			}
			return fmt.Errorf("describe image %s: %w", amiID, err)
		}

		if len(resp.Images) == 0 {
			// Just after CreateImage the image may not be queryable yet (eventual consistency).
			// But once it has appeared, an empty result means it was deleted - don't wait for an image that can never finish.
			if seen {
				return fmt.Errorf("AMI %s disappeared mid-build (deregistered or deleted?)", amiID)
			}
			slog.Info("waiting for AMI", "ami", amiID, "state", "not-yet-visible", "attempt", attempt)
			if err := sleepCtx(ctx, interval); err != nil {
				return waitStopped(ctx, amiID, "not-yet-visible")
			}
			continue
		}

		seen = true
		img := resp.Images[0]
		reason := ""
		if sr := img.StateReason; sr != nil {
			reason = awssdk.ToString(sr.Message)
		}
		switch img.State {
		case ec2types.ImageStateAvailable:
			slog.Info("AMI is available", "ami", amiID, "attempts", attempt)
			return nil
		case ec2types.ImageStateFailed, ec2types.ImageStateError, ec2types.ImageStateInvalid,
			ec2types.ImageStateDeregistered, ec2types.ImageStateDisabled:
			return fmt.Errorf("AMI %s entered state %q: %s", amiID, img.State, reason)
		}

		slog.Info("waiting for AMI", "ami", amiID, "state", string(img.State), "attempt", attempt)
		if err := sleepCtx(ctx, interval); err != nil {
			return waitStopped(ctx, amiID, string(img.State))
		}
	}
}

// sleepCtx waits for d, or returns ctx.Err() if the context finishes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// waitStopped reports why a wait ended: the amiMaxWait deadline vs an external cancel (Ctrl-C / SIGTERM) - so the
// message doesn't claim a 30-minute timeout when the user cancelled after a few seconds.
func waitStopped(ctx context.Context, amiID, lastState string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("AMI %s not available after %s (last state %q)", amiID, amiMaxWait, lastState)
	}
	return fmt.Errorf("waiting for AMI %s cancelled (last state %q): %w", amiID, lastState, ctx.Err())
}

// imageNotFound reports whether err is the EC2 InvalidAMIID.NotFound API error (the image was deregistered/deleted).
func imageNotFound(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidAMIID.NotFound"
}

// sourceInstanceID resolves the instance to image, in precedence order: an explicit instance_id, else the first running
// instance matching source_tags, else the first InService instance in the ASG.
func (p *amiPlugin) sourceInstanceID(ctx context.Context, ap amiCreateParams) (string, error) {
	if ap.InstanceID != "" {
		return ap.InstanceID, nil
	}
	if len(ap.SourceTags) > 0 {
		filters := []ec2types.Filter{{Name: awssdk.String("instance-state-name"), Values: []string{"running"}}}
		for k, v := range ap.SourceTags {
			filters = append(filters, ec2types.Filter{Name: awssdk.String("tag:" + k), Values: []string{v}})
		}
		out, err := p.ec2.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{Filters: filters})
		if err != nil {
			return "", fmt.Errorf("describe instances: %w", err)
		}
		for _, r := range out.Reservations {
			for _, inst := range r.Instances {
				if inst.InstanceId != nil {
					return *inst.InstanceId, nil
				}
			}
		}
		return "", fmt.Errorf("no running instance matches source_tags %v", ap.SourceTags)
	}

	grp, err := p.fetchASG(ctx, ap.ASG)
	if err != nil {
		return "", err
	}
	for _, inst := range grp.Instances {
		if inst.LifecycleState == astypes.LifecycleStateInService && inst.InstanceId != nil {
			return *inst.InstanceId, nil
		}
	}
	return "", fmt.Errorf("no InService instance in ASG %q", ap.ASG)
}

// describeInstance returns the full instance record (for its tags) by id.
func (p *amiPlugin) describeInstance(ctx context.Context, id string) (ec2types.Instance, error) {
	out, err := p.ec2.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		return ec2types.Instance{}, fmt.Errorf("describe instance %s: %w", id, err)
	}
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			return inst, nil
		}
	}
	return ec2types.Instance{}, fmt.Errorf("instance %s not found", id)
}

// amiTags copies the source instance's tags onto the image (and its snapshots), dropping the Name tag and AWS-managed
// (aws:*) tags, then sets Name to the AMI's own name.
func amiTags(instanceTags []ec2types.Tag, amiName string) []ec2types.Tag {
	var tags []ec2types.Tag
	for _, t := range instanceTags {
		k := awssdk.ToString(t.Key)
		if k == "Name" || strings.HasPrefix(k, "aws:") {
			continue
		}
		tags = append(tags, ec2types.Tag{Key: t.Key, Value: t.Value})
	}
	return append(tags, ec2types.Tag{Key: awssdk.String("Name"), Value: awssdk.String(amiName)})
}

// patchLaunchTemplate creates a new launch template version (cloned from $Default) with the AMI as its image and makes
// it the default version, so the next instance refresh launches from the new AMI.
func (p *amiPlugin) patchLaunchTemplate(ctx context.Context, tgt amiLTTarget, amiID string) error {
	ltID := tgt.ID
	if ltID == "" {
		if tgt.ASG == "" {
			return fmt.Errorf("launch_template: set 'id' or 'asg'")
		}
		grp, err := p.fetchASG(ctx, tgt.ASG)
		if err != nil {
			return err
		}
		if ltID = asgLaunchTemplateID(grp); ltID == "" {
			return fmt.Errorf("ASG %q has no launch template", tgt.ASG)
		}
	}

	slog.Info("patching launch template", "launch_template", ltID, "ami", amiID)
	ver, err := p.ec2.CreateLaunchTemplateVersion(ctx, &awsec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId:   awssdk.String(ltID),
		SourceVersion:      awssdk.String("$Default"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{ImageId: awssdk.String(amiID)},
	})
	if err != nil {
		return fmt.Errorf("create launch template version: %w", err)
	}
	version := strconv.FormatInt(awssdk.ToInt64(ver.LaunchTemplateVersion.VersionNumber), 10)
	if _, err := p.ec2.ModifyLaunchTemplate(ctx, &awsec2.ModifyLaunchTemplateInput{
		LaunchTemplateId: awssdk.String(ltID),
		DefaultVersion:   awssdk.String(version),
	}); err != nil {
		return fmt.Errorf("set default launch template version: %w", err)
	}
	slog.Info("launch template updated", "launch_template", ltID, "default_version", version)
	return nil
}

// asgLaunchTemplateID returns the launch template id attached to a group, whether directly or via a mixed-instances
// policy.
func asgLaunchTemplateID(grp astypes.AutoScalingGroup) string {
	if grp.LaunchTemplate != nil && grp.LaunchTemplate.LaunchTemplateId != nil {
		return *grp.LaunchTemplate.LaunchTemplateId
	}
	if mip := grp.MixedInstancesPolicy; mip != nil && mip.LaunchTemplate != nil &&
		mip.LaunchTemplate.LaunchTemplateSpecification != nil &&
		mip.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateId != nil {
		return *mip.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateId
	}
	return ""
}

// fetchASG returns the named Auto Scaling group.
func (p *amiPlugin) fetchASG(ctx context.Context, name string) (astypes.AutoScalingGroup, error) {
	out, err := p.asg.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{name},
	})
	if err != nil {
		return astypes.AutoScalingGroup{}, fmt.Errorf("describe ASG %q: %w", name, err)
	}
	if len(out.AutoScalingGroups) == 0 {
		return astypes.AutoScalingGroup{}, fmt.Errorf("ASG %q not found", name)
	}
	return out.AutoScalingGroups[0], nil
}

// runCleanup deregisters self-owned AMIs matching the filters - Name starting with "<name_prefix>-" and/or the given
// tags - keeping the newest keep_last (default 3; values < 1 fall back to 3), and deletes their backing snapshots.
// At least one filter is required so a misconfiguration can't prune every image.
// It is best-effort: per-image failures are reported but do not abort the action.
// Progress is whoosh narrative and goes through slog.
func (p *amiPlugin) runCleanup(ctx context.Context, params map[string]any, _ io.Writer) error {
	var ap amiCleanupParams
	if err := awsparams.DecodeFeature(p.defaults, params, &ap); err != nil {
		return err
	}
	if ap.NamePrefix == "" && len(ap.Tags) == 0 {
		return fmt.Errorf("%s: set 'name_prefix' and/or 'tags' to choose which images to prune", actionAMICleanup)
	}
	keep := ap.KeepLast
	if keep < 1 {
		keep = 3
	}

	// Tags filter server-side (match any value per key, AND across keys), name_prefix is matched on the image Name below.
	in := &awsec2.DescribeImagesInput{Owners: []string{"self"}}
	for k, vals := range ap.Tags {
		if len(vals) == 0 {
			continue
		}
		in.Filters = append(in.Filters, ec2types.Filter{Name: awssdk.String("tag:" + k), Values: vals})
	}
	resp, err := p.ec2.DescribeImages(ctx, in)
	if err != nil {
		return fmt.Errorf("%s: describe images: %w", actionAMICleanup, err)
	}
	var images []ec2types.Image
	for _, img := range resp.Images {
		if ap.NamePrefix != "" && !strings.HasPrefix(awssdk.ToString(img.Name), ap.NamePrefix+"-") {
			continue
		}
		images = append(images, img)
	}
	// Timestamp-formatted ISO dates sort chronologically as strings, newest first.
	sort.Slice(images, func(i, j int) bool {
		return awssdk.ToString(images[i].CreationDate) > awssdk.ToString(images[j].CreationDate)
	})
	if len(images) <= keep {
		slog.Info("AMI cleanup: nothing to remove", "matching", len(images), "keep_last", keep)
		return nil
	}

	old := images[keep:]
	warnings := 0
	for _, img := range old {
		id := awssdk.ToString(img.ImageId)
		slog.Info("deregistering old AMI", "ami", id, "name", awssdk.ToString(img.Name))
		if _, err := p.ec2.DeregisterImage(ctx, &awsec2.DeregisterImageInput{ImageId: img.ImageId}); err != nil {
			warnings++
			slog.Warn("deregister AMI failed", "ami", id, "error", err)
			continue
		}
		for _, bdm := range img.BlockDeviceMappings {
			if bdm.Ebs == nil || bdm.Ebs.SnapshotId == nil {
				continue
			}
			snap := *bdm.Ebs.SnapshotId
			if _, err := p.ec2.DeleteSnapshot(ctx, &awsec2.DeleteSnapshotInput{SnapshotId: bdm.Ebs.SnapshotId}); err != nil {
				warnings++
				slog.Warn("delete snapshot failed", "snapshot", snap, "error", err)
				continue
			}
			slog.Info("deleted snapshot", "snapshot", snap)
		}
	}
	slog.Info("AMI cleanup done", "removed", len(old), "kept", keep, "warnings", warnings)
	return nil
}
