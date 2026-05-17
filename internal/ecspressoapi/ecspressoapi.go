// Package ecspressoapi is a thin wrapper around github.com/kayac/ecspresso/v2.
// It exists so the provider package depends on a stable, small surface area
// rather than ecspresso's entire public API, and so the tfstate-plugin
// injection point (Phase 5) can be added in one place.
package ecspressoapi

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/fujiwara/tfstate-lookup/tfstate"
	ecspresso "github.com/kayac/ecspresso/v2"
)

// ServiceInfo is the subset of ECS service attributes the provider exposes
// as computed Terraform attributes. task_definition_* are intentionally
// omitted: they advance on every ecspresso deploy (CLI or otherwise) and
// Terraform cannot keep them authoritative.
type ServiceInfo struct {
	ServiceArn  string
	ServiceName string
	ClusterArn  string
	ClusterName string
}

// Deploy invokes ecspresso deploy only when there is a real diff
// between the locally-rendered definitions and what is currently
// deployed. The `deployed` return is true when ecspresso.App.Deploy
// was actually called and false when it was skipped because
// ecspresso.App.HasDiff returned no diff. tfstateOverrides are
// applied to the tfstate plugin identified by tfstateFuncPrefix
// before the diff is computed, so the comparison sees the same
// rendered definitions a real deploy would.
func Deploy(ctx context.Context, configPath string, tfstateFuncPrefix string, tfstateOverrides map[string]any) (info *ServiceInfo, deployed bool, err error) {
	app, err := newApp(ctx, configPath)
	if err != nil {
		return nil, false, err
	}
	configureTFStatePlugin(app, tfstateFuncPrefix, tfstateOverrides)

	hasDiff, err := app.HasDiff(ctx, ecspresso.DiffOption{
		Unified:     true,
		WithService: true,
	})
	if err != nil {
		return nil, false, err
	}
	if !hasDiff {
		// No-op against AWS: the rendered configs already match the
		// deployed state. Return the current service info so the
		// caller can update computed attributes without claiming a
		// new deploy happened.
		info, err = describe(ctx, app)
		return info, false, err
	}

	if err := app.Deploy(ctx, ecspresso.DeployOption{
		Wait:          true,
		UpdateService: true,
		// DesiredCount must be set to ecspresso.DefaultDesiredCount
		// (-1) so that ecspresso's calcDesiredCount falls back to the
		// value defined in the service definition. When the CLI runs
		// ecspresso, kong's `default:"-1"` does this automatically;
		// the library API leaves the field nil, in which case
		// calcDesiredCount returns nil and CreateService rejects the
		// request with "DesiredCount is missing".
		DesiredCount: aws.Int32(ecspresso.DefaultDesiredCount),
	}); err != nil {
		return nil, false, err
	}
	info, err = describe(ctx, app)
	return info, true, err
}

// Describe returns the current service info without deploying. tfstate
// overrides are not required because DescribeService does not re-render
// the task/service definition templates.
func Describe(ctx context.Context, configPath string) (*ServiceInfo, error) {
	app, err := newApp(ctx, configPath)
	if err != nil {
		return nil, err
	}
	return describe(ctx, app)
}

// Delete runs ecspresso delete with Force (skip prompt) and Terminate
// (DeleteService force=true, i.e. scale-to-zero + drain + delete).
// tfstateOverrides are forwarded so any plugin-rendered config the
// delete path may read still resolves correctly.
func Delete(ctx context.Context, configPath string, tfstateFuncPrefix string, tfstateOverrides map[string]any) error {
	app, err := newApp(ctx, configPath)
	if err != nil {
		return err
	}
	configureTFStatePlugin(app, tfstateFuncPrefix, tfstateOverrides)
	return app.Delete(ctx, ecspresso.DeleteOption{
		Force:     true,
		Terminate: true,
	})
}

// configureTFStatePlugin pushes the caller-supplied overrides into
// the tfstate plugin identified by funcPrefix and then discards the
// scanned tfstate so Lookup serves keys from those overrides only.
//
// The provider's design treats `tfstate_values` as the complete set
// of tfstate-shaped inputs to ecspresso; resolving a missing key
// from a possibly-stale tfstate file would let Terraform-unaware
// changes leak into a deploy. After this call, ecspresso's
// `tfstate(...)` lookups against any key not present in
// `tfstate_values` fail fast with "is not found in tfstate", which
// is exactly the early signal we want.
//
// No-op when no matching tfstate plugin is configured (ecspresso.yml
// may legitimately have no tfstate plugin at all).
func configureTFStatePlugin(app *ecspresso.App, funcPrefix string, overrides map[string]any) {
	state, ok := app.PluginInstance("tfstate", funcPrefix).(*tfstate.TFState)
	if !ok {
		return
	}
	if len(overrides) > 0 {
		state.SetOverrides(overrides)
	}
	state.DiscardScannedState()
}

// IsNotFound reports whether err indicates the ECS service does not exist.
// Used by Read to decide whether to remove the resource from Terraform state.
func IsNotFound(err error) bool {
	var nf ecspresso.ErrNotFound
	return errors.As(err, &nf)
}

func newApp(ctx context.Context, configPath string) (*ecspresso.App, error) {
	cliOpts := &ecspresso.CLIOptions{
		ConfigFilePath: configPath,
	}
	return ecspresso.New(ctx, cliOpts)
}

func describe(ctx context.Context, app *ecspresso.App) (*ServiceInfo, error) {
	sv, err := app.DescribeService(ctx)
	if err != nil {
		return nil, err
	}
	return &ServiceInfo{
		ServiceArn:  deref(sv.ServiceArn),
		ServiceName: deref(sv.ServiceName),
		ClusterArn:  deref(sv.ClusterArn),
		ClusterName: arnLastSegment(deref(sv.ClusterArn)),
	}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// arnLastSegment returns the substring after the final '/'.
// "arn:aws:ecs:us-east-1:123:cluster/my-cluster" -> "my-cluster".
// Returns the input unchanged if no slash is present.
func arnLastSegment(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
