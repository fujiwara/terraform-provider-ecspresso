// Package ecspressoapi is a thin wrapper around github.com/kayac/ecspresso/v2.
// It exists so the provider package depends on a stable, small surface area
// rather than ecspresso's entire public API, and so the tfstate-plugin
// injection point can be expressed in one place.
package ecspressoapi

import (
	"context"
	"errors"
	"fmt"
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

// Deploy invokes ecspresso deploy only when App.HasDiff reports a real
// diff against AWS; deployed is true only when App.Deploy actually ran.
// tfstateOverrides drive every tfstate(...) lookup (see newApp). warnings
// holds non-fatal advisories to surface — currently a func_prefix mismatch
// (see funcPrefixWarning).
func Deploy(ctx context.Context, configPath string, tfstateFuncPrefix string, tfstateOverrides map[string]any) (info *ServiceInfo, deployed bool, warnings []string, err error) {
	app, err := newApp(ctx, configPath, tfstateFuncPrefix, tfstateOverrides)
	if err != nil {
		return nil, false, nil, err
	}
	if w := funcPrefixWarning(app.Config().Plugins, tfstateFuncPrefix); w != "" {
		warnings = append(warnings, w)
	}

	hasDiff, err := app.HasDiff(ctx, ecspresso.DiffOption{
		WithService: true,
	})
	if err != nil {
		return nil, false, warnings, err
	}
	if !hasDiff {
		// No-op against AWS: the rendered configs already match the
		// deployed state. Return the current service info so the
		// caller can update computed attributes without claiming a
		// new deploy happened.
		info, err = describe(ctx, app)
		return info, false, warnings, err
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
		return nil, false, warnings, err
	}
	info, err = describe(ctx, app)
	return info, true, warnings, err
}

// Describe returns the current service info without deploying.
// tfstateOverrides are still required because the ecspresso config may
// reference `tfstate(...)` for top-level fields like `cluster` /
// `service` (resolved during config load).
func Describe(ctx context.Context, configPath string, tfstateFuncPrefix string, tfstateOverrides map[string]any) (*ServiceInfo, error) {
	app, err := newApp(ctx, configPath, tfstateFuncPrefix, tfstateOverrides)
	if err != nil {
		return nil, err
	}
	return describe(ctx, app)
}

// Delete runs ecspresso delete with Force (skip prompt) and Terminate
// (DeleteService force=true, i.e. scale-to-zero + drain + delete).
// tfstateOverrides are forwarded so config-level and definition-level
// `tfstate(...)` lookups all resolve from the same Terraform-managed
// source.
func Delete(ctx context.Context, configPath string, tfstateFuncPrefix string, tfstateOverrides map[string]any) error {
	app, err := newApp(ctx, configPath, tfstateFuncPrefix, tfstateOverrides)
	if err != nil {
		return err
	}
	return app.Delete(ctx, ecspresso.DeleteOption{
		Force:     true,
		Terminate: true,
	})
}

// IsNotFound reports whether err indicates the ECS service does not exist.
// Used by Read to decide whether to remove the resource from Terraform state.
func IsNotFound(err error) bool {
	return errors.Is(err, ecspresso.ErrNotFound)
}

// ConfigLoadError wraps a failure to construct the App (loading/rendering
// the config, including config-level tfstate(...) lookups). Kept distinct
// from a later AWS API error so Read can skip the refresh instead of
// failing the plan when committed tfstate_values lag a pending change
// (e.g. a reference to a resource created in the same apply).
type ConfigLoadError struct{ err error }

func (e *ConfigLoadError) Error() string { return e.err.Error() }
func (e *ConfigLoadError) Unwrap() error { return e.err }

// IsConfigLoadError reports whether err originated from loading /
// rendering the ecspresso config (newApp), as opposed to an AWS API call.
func IsConfigLoadError(err error) bool {
	var c *ConfigLoadError
	return errors.As(err, &c)
}

// funcPrefixWarning flags a likely tfstate_func_prefix typo: tfstate_values
// is injected at one prefix, but the config's tfstate plugins all use
// others, so those lookups silently read from a file instead of
// tfstate_values. Returns "" when no tfstate plugin is declared (the
// intended plugins-less mode) or when the injected prefix matches one.
// Advisory, not fatal — a different-prefix plugin may be a deliberate
// "read another tfstate from a file" setup.
func funcPrefixWarning(plugins []ecspresso.ConfigPlugin, tfstateFuncPrefix string) string {
	var declared []string
	for _, p := range plugins {
		if p.Name != "tfstate" {
			continue
		}
		if p.FuncPrefix == tfstateFuncPrefix {
			return "" // the injected prefix lines up with a declared plugin
		}
		declared = append(declared, fmt.Sprintf("%q", p.FuncPrefix))
	}
	if len(declared) == 0 {
		return "" // plugins-less mode: nothing to mismatch against
	}
	return fmt.Sprintf(
		"tfstate_values is injected at tfstate_func_prefix=%q, but the ecspresso config declares tfstate plugin(s) with func_prefix %s and none matches. "+
			"Lookups at those prefixes are read from the plugin's on-disk/S3 tfstate, not from tfstate_values. "+
			"If you meant to feed one of them, set tfstate_func_prefix to its func_prefix; if the func_prefix=%q lookups are supplied only by tfstate_values, you can ignore this.",
		tfstateFuncPrefix, strings.Join(declared, ", "), tfstateFuncPrefix,
	)
}

// newApp constructs an ecspresso App with an in-memory tfstate plugin
// pre-populated from tfstateOverrides. ecspresso.New skips the configured
// tfstate plugin's Setup (no file / S3 read) and resolves every tfstate(...)
// lookup — config-level and in task / service definitions — from the
// overrides only. tfstate_values is the complete input by design: a missing
// key fails fast with "is not found in tfstate" instead of silently falling
// back to a possibly-stale file.
func newApp(ctx context.Context, configPath, tfstateFuncPrefix string, overrides map[string]any) (*ecspresso.App, error) {
	state := tfstate.Empty()
	if len(overrides) > 0 {
		state.SetOverrides(overrides)
	}
	cliOpts := &ecspresso.CLIOptions{
		ConfigFilePath: configPath,
	}
	app, err := ecspresso.New(ctx, cliOpts,
		ecspresso.WithPluginInstance("tfstate", tfstateFuncPrefix, state),
	)
	if err != nil {
		// Wrap so Read can distinguish a config load/render failure (which
		// it can safely skip during refresh) from an AWS API error.
		return nil, &ConfigLoadError{err: err}
	}
	return app, nil
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
