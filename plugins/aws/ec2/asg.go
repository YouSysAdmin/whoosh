package ec2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awsparams "github.com/yousysadmin/whoosh/plugins/aws/internal/params"
)

// asgAPI is the slice of the Auto Scaling client the plugin uses.
// DescribeAuto- ScalingGroups is used by the rollback action to find the launch template attached to a group.
type asgAPI interface {
	StartInstanceRefresh(ctx context.Context, in *autoscaling.StartInstanceRefreshInput, optFns ...func(*autoscaling.Options)) (*autoscaling.StartInstanceRefreshOutput, error)
	CancelInstanceRefresh(ctx context.Context, in *autoscaling.CancelInstanceRefreshInput, optFns ...func(*autoscaling.Options)) (*autoscaling.CancelInstanceRefreshOutput, error)
	DescribeInstanceRefreshes(ctx context.Context, in *autoscaling.DescribeInstanceRefreshesInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeInstanceRefreshesOutput, error)
	DescribeAutoScalingGroups(ctx context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

// asgEC2API is the slice of the EC2 client the rollback action uses to move a launch template back to its previous
// version.
type asgEC2API interface {
	DescribeLaunchTemplateVersions(ctx context.Context, in *awsec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error)
	CreateLaunchTemplateVersion(ctx context.Context, in *awsec2.CreateLaunchTemplateVersionInput, optFns ...func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error)
	ModifyLaunchTemplate(ctx context.Context, in *awsec2.ModifyLaunchTemplateInput, optFns ...func(*awsec2.Options)) (*awsec2.ModifyLaunchTemplateOutput, error)
}

const (
	// Action names, namespaced under the plugins ("<plugins>:<action>").
	actionASGRefresh  = FeatureASG + ":refresh"
	actionASGRollback = FeatureASG + ":rollback"
	// asgPollInterval is how often the refresh action polls (and logs) status while waiting for the refresh to finish.
	asgPollInterval = 30 * time.Second

	// Default refresh preferences.
	defMinHealthyPercentage int32 = 100
	defMaxHealthyPercentage int32 = 200
	defInstanceWarmup       int32 = 300
	defSkipMatching               = true
	defAutoRollback               = false
)

// asgPlugin registers the aws:ec2:asg:refresh and aws:ec2:asg:rollback actions.
// Refresh rolls the group onto its current launch template, rollback first copies the launch template's previous
// version forward (the manual counterpart to the instance refresh's AutoRollback) and then refreshes onto it.
type asgPlugin struct {
	api asgAPI
	ec2 asgEC2API
	// defaults are the feature-level params (the `aws:ec2:asg` actions: entry), layered under each task's `with:` (the
	// task wins).
	defaults map[string]any
	// pollInterval is how often run polls refresh status, zero means asgPollInterval.
	// Overridden in tests to avoid real sleeps.
	pollInterval time.Duration
}

// asgRefreshParams are the refresh preferences (a task's `with:`), each is optional and falls back to the documented
// default.
type asgRefreshParams struct {
	Name                 string `yaml:"name"`
	MinHealthyPercentage *int32 `yaml:"min_healthy_percentage"` // default 100
	MaxHealthyPercentage *int32 `yaml:"max_healthy_percentage"` // default 200
	InstanceWarmup       *int32 `yaml:"instance_warmup"`        // default 300 (seconds)
	SkipMatching         *bool  `yaml:"skip_matching"`          // default true
	AutoRollback         *bool  `yaml:"auto_rollback"`          // default false
}

// asgRollbackParams are the aws:ec2:asg:rollback action params.
// It rolls the launch template back to its previous version, then refreshes (with the same preferences as
// aws:ec2:asg:refresh).
type asgRollbackParams struct {
	asgRefreshParams `yaml:",inline"`
	// LaunchTemplate selects which launch template to roll back: an explicit id, or the template of an ASG.
	// When omitted, the template attached to `name` (the ASG being refreshed) is used.
	LaunchTemplate *amiLTTarget `yaml:"launch_template"`
	// SetDefault makes the rolled-back version the launch template's $Default.
	// Default true, so a group that launches from $Default picks it up (a group that tracks $Latest does so regardless,
	// since the copy becomes the latest).
	SetDefault *bool `yaml:"set_default"`
}

func (a *asgPlugin) runRefresh(ctx context.Context, params map[string]any, _ io.Writer) error {
	var ap asgRefreshParams
	if err := awsparams.DecodeFeature(a.defaults, params, &ap); err != nil {
		return err
	}
	if ap.Name == "" {
		return fmt.Errorf("%s: 'name' is required", actionASGRefresh)
	}
	return a.startAndWait(ctx, ap, false)
}

// runRollback copies the launch template's previous version forward to a new latest version (optionally making it the
// default), then starts and waits for an instance refresh so the group relaunches on the rolled-back configuration.
func (a *asgPlugin) runRollback(ctx context.Context, params map[string]any, _ io.Writer) error {
	var rp asgRollbackParams
	if err := awsparams.DecodeFeature(a.defaults, params, &rp); err != nil {
		return err
	}
	if rp.Name == "" {
		return fmt.Errorf("%s: 'name' is required", actionASGRollback)
	}

	ltID, err := a.rollbackLaunchTemplateID(ctx, rp)
	if err != nil {
		return fmt.Errorf("%s: %w", actionASGRollback, err)
	}
	prev, err := a.previousLTVersion(ctx, ltID)
	if err != nil {
		return fmt.Errorf("%s: %w", actionASGRollback, err)
	}

	slog.Info("rolling back launch template", "launch_template", ltID, "to_version", prev, "asg", rp.Name)
	// SourceVersion clones the previous version, an empty LaunchTemplateData means "no overrides", so the new (latest)
	// version is an exact copy of it.
	ver, err := a.ec2.CreateLaunchTemplateVersion(ctx, &awsec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId:   awssdk.String(ltID),
		SourceVersion:      awssdk.String(strconv.FormatInt(prev, 10)),
		VersionDescription: awssdk.String(fmt.Sprintf("rollback to v%d", prev)),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{},
	})
	if err != nil {
		return fmt.Errorf("%s: create launch template version: %w", actionASGRollback, err)
	}
	newVer := awssdk.ToInt64(ver.LaunchTemplateVersion.VersionNumber)

	setDefault := rp.SetDefault == nil || *rp.SetDefault
	if setDefault {
		if _, err := a.ec2.ModifyLaunchTemplate(ctx, &awsec2.ModifyLaunchTemplateInput{
			LaunchTemplateId: awssdk.String(ltID),
			DefaultVersion:   awssdk.String(strconv.FormatInt(newVer, 10)),
		}); err != nil {
			return fmt.Errorf("%s: set default launch template version: %w", actionASGRollback, err)
		}
	}
	slog.Info("launch template rolled back", "launch_template", ltID,
		"new_version", newVer, "copied_from", prev, "default", setDefault)

	// Cancel a refresh already in flight: rollback typically happens while the bad deploy's own refresh is still
	// rolling the fleet onto the configuration being rolled back - skipping it would report success while the bad
	// version keeps deploying.
	return a.startAndWait(ctx, rp.asgRefreshParams, true)
}

// startAndWait starts an instance refresh with the given preferences and blocks until it finishes.
// A refresh already running is logged and skipped (not fatal) unless cancelInFlight is set, in which case it is
// cancelled and this refresh started in its place once the cancellation settles.
func (a *asgPlugin) startAndWait(ctx context.Context, ap asgRefreshParams, cancelInFlight bool) error {
	in := &autoscaling.StartInstanceRefreshInput{
		AutoScalingGroupName: awssdk.String(ap.Name),
		Preferences: &astypes.RefreshPreferences{
			MinHealthyPercentage: awsparams.OrPtr(ap.MinHealthyPercentage, defMinHealthyPercentage),
			MaxHealthyPercentage: awsparams.OrPtr(ap.MaxHealthyPercentage, defMaxHealthyPercentage),
			InstanceWarmup:       awsparams.OrPtr(ap.InstanceWarmup, defInstanceWarmup),
			SkipMatching:         awsparams.OrPtr(ap.SkipMatching, defSkipMatching),
			AutoRollback:         awsparams.OrPtr(ap.AutoRollback, defAutoRollback),
		},
	}

	res, err := a.api.StartInstanceRefresh(ctx, in)
	if _, inProgress := errors.AsType[*astypes.InstanceRefreshInProgressFault](err); inProgress {
		if !cancelInFlight {
			slog.Warn("instance refresh already in progress, skipping", "asg", ap.Name)
			return nil
		}
		if err := a.cancelActiveRefresh(ctx, ap.Name); err != nil {
			return err
		}
		res, err = a.api.StartInstanceRefresh(ctx, in)
	}
	if err != nil {
		return fmt.Errorf("start instance refresh: %w", err)
	}
	id := awssdk.ToString(res.InstanceRefreshId)
	slog.Info("started instance refresh", "asg", ap.Name, "refresh_id", id,
		"min_healthy", awssdk.ToInt32(in.Preferences.MinHealthyPercentage),
		"max_healthy", awssdk.ToInt32(in.Preferences.MaxHealthyPercentage),
		"warmup", awssdk.ToInt32(in.Preferences.InstanceWarmup),
		"skip_matching", awssdk.ToBool(in.Preferences.SkipMatching))

	return a.waitForRefresh(ctx, ap.Name, id)
}

// cancelActiveRefresh cancels the ASG's in-flight instance refresh and waits until it reaches a terminal state, so a
// new refresh can be started in its place.
func (a *asgPlugin) cancelActiveRefresh(ctx context.Context, name string) error {
	slog.Warn("cancelling in-flight instance refresh", "asg", name)
	if _, err := a.api.CancelInstanceRefresh(ctx, &autoscaling.CancelInstanceRefreshInput{
		AutoScalingGroupName: awssdk.String(name),
	}); err != nil {
		// The refresh finished between the failed start and the cancel - nothing left to wait for.
		if _, gone := errors.AsType[*astypes.ActiveInstanceRefreshNotFoundFault](err); gone {
			return nil
		}
		return fmt.Errorf("cancel instance refresh: %w", err)
	}

	interval := a.pollInterval
	if interval <= 0 {
		interval = asgPollInterval
	}
	for {
		out, err := a.api.DescribeInstanceRefreshes(ctx, &autoscaling.DescribeInstanceRefreshesInput{
			AutoScalingGroupName: awssdk.String(name),
			MaxRecords:           awssdk.Int32(1),
		})
		if err != nil {
			return fmt.Errorf("describe instance refreshes: %w", err)
		}
		if len(out.InstanceRefreshes) == 0 {
			return nil
		}
		switch st := out.InstanceRefreshes[0].Status; st {
		case astypes.InstanceRefreshStatusSuccessful,
			astypes.InstanceRefreshStatusFailed,
			astypes.InstanceRefreshStatusCancelled,
			astypes.InstanceRefreshStatusRollbackFailed,
			astypes.InstanceRefreshStatusRollbackSuccessful:
			return nil
		default:
			slog.Info("waiting for in-flight instance refresh to cancel", "asg", name, "status", string(st))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped waiting for instance refresh cancellation: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}

// rollbackLaunchTemplateID resolves which launch template to roll back: an explicit id, the template of a named ASG, or
// (by default) the template attached to the ASG being refreshed.
func (a *asgPlugin) rollbackLaunchTemplateID(ctx context.Context, rp asgRollbackParams) (string, error) {
	if rp.LaunchTemplate != nil && rp.LaunchTemplate.ID != "" {
		return rp.LaunchTemplate.ID, nil
	}
	asgName := rp.Name
	if rp.LaunchTemplate != nil && rp.LaunchTemplate.ASG != "" {
		asgName = rp.LaunchTemplate.ASG
	}
	out, err := a.api.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{asgName},
	})
	if err != nil {
		return "", fmt.Errorf("describe ASG %q: %w", asgName, err)
	}
	if len(out.AutoScalingGroups) == 0 {
		return "", fmt.Errorf("ASG %q not found", asgName)
	}
	ltID := asgLaunchTemplateID(out.AutoScalingGroups[0])
	if ltID == "" {
		return "", fmt.Errorf("ASG %q has no launch template", asgName)
	}
	return ltID, nil
}

// previousLTVersion returns the version number just below the latest, listing all versions (which need not be
// contiguous if some were deleted) and taking the second-highest. It errors when fewer than two versions exist.
func (a *asgPlugin) previousLTVersion(ctx context.Context, ltID string) (int64, error) {
	var nums []int64
	var token *string
	for {
		out, err := a.ec2.DescribeLaunchTemplateVersions(ctx, &awsec2.DescribeLaunchTemplateVersionsInput{
			LaunchTemplateId: awssdk.String(ltID),
			NextToken:        token,
		})
		if err != nil {
			return 0, fmt.Errorf("describe launch template versions: %w", err)
		}
		for _, v := range out.LaunchTemplateVersions {
			nums = append(nums, awssdk.ToInt64(v.VersionNumber))
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(nums) < 2 {
		return 0, fmt.Errorf("launch template %s has %d version(s), need at least 2 to roll back", ltID, len(nums))
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] > nums[j] })
	return nums[1], nil
}

// waitForRefresh polls the instance refresh until it reaches a terminal state, logging status and progress each
// interval so a long rollout isn't mistaken for a hang. A non-successful terminal state returns an error.
// There is no internal timeout - the refresh runs as long as it takes, cancel the context to stop waiting.
func (a *asgPlugin) waitForRefresh(ctx context.Context, asgName, id string) error {
	interval := a.pollInterval
	if interval <= 0 {
		interval = asgPollInterval
	}

	for {
		resp, err := a.api.DescribeInstanceRefreshes(ctx, &autoscaling.DescribeInstanceRefreshesInput{
			AutoScalingGroupName: awssdk.String(asgName),
			InstanceRefreshIds:   []string{id},
		})
		if err != nil {
			return fmt.Errorf("describe instance refresh %s: %w", id, err)
		}
		if len(resp.InstanceRefreshes) == 0 {
			return fmt.Errorf("instance refresh %s not found", id)
		}
		r := resp.InstanceRefreshes[0]
		pct := awssdk.ToInt32(r.PercentageComplete)
		reason := awssdk.ToString(r.StatusReason)

		switch r.Status {
		case astypes.InstanceRefreshStatusSuccessful:
			slog.Info("instance refresh complete", "asg", asgName, "refresh_id", id)
			return nil
		case astypes.InstanceRefreshStatusFailed,
			astypes.InstanceRefreshStatusCancelled,
			astypes.InstanceRefreshStatusRollbackFailed,
			astypes.InstanceRefreshStatusRollbackSuccessful:
			return fmt.Errorf("instance refresh %s %s: %s", id, r.Status, reason)
		}

		slog.Info("instance refresh in progress", "asg", asgName, "refresh_id", id,
			"status", string(r.Status), "percent", pct)
		select {
		case <-ctx.Done():
			// Cancelling (Ctrl-C / SIGTERM) only stops us waiting - the refresh keeps running in AWS, it isn't cancelled here.
			return fmt.Errorf("stopped waiting for instance refresh %s (it continues in AWS): %w", id, ctx.Err())
		case <-time.After(interval):
		}
	}
}
