// Package ecspressoapi is a thin wrapper around github.com/kayac/ecspresso/v2.
// It exists so the provider package depends on a stable, small surface area
// rather than ecspresso's entire public API, and so the tfstate-plugin
// injection point (Phase 5) can be added in one place.
package ecspressoapi

import (
	"context"
	"errors"
	"strconv"
	"strings"

	ecspresso "github.com/kayac/ecspresso/v2"
)

// ServiceInfo is the subset of ECS service attributes the provider exposes
// as computed Terraform attributes.
type ServiceInfo struct {
	ServiceArn             string
	ServiceName            string
	ClusterArn             string
	ClusterName            string
	TaskDefinitionArn      string
	TaskDefinitionFamily   string
	TaskDefinitionRevision int64
}

// Deploy invokes ecspresso deploy and returns the post-deploy service info.
func Deploy(ctx context.Context, configPath string) (*ServiceInfo, error) {
	app, err := newApp(ctx, configPath)
	if err != nil {
		return nil, err
	}
	if err := app.Deploy(ctx, ecspresso.DeployOption{
		Wait:          true,
		UpdateService: true,
	}); err != nil {
		return nil, err
	}
	return describe(ctx, app)
}

// Describe returns the current service info without deploying.
func Describe(ctx context.Context, configPath string) (*ServiceInfo, error) {
	app, err := newApp(ctx, configPath)
	if err != nil {
		return nil, err
	}
	return describe(ctx, app)
}

// Delete runs ecspresso delete with Force (skip prompt) and Terminate
// (DeleteService force=true, i.e. scale-to-zero + drain + delete).
func Delete(ctx context.Context, configPath string) error {
	app, err := newApp(ctx, configPath)
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
	info := &ServiceInfo{
		ServiceArn:        deref(sv.ServiceArn),
		ServiceName:       deref(sv.ServiceName),
		ClusterArn:        deref(sv.ClusterArn),
		ClusterName:       arnLastSegment(deref(sv.ClusterArn)),
		TaskDefinitionArn: deref(sv.TaskDefinition),
	}
	if family, rev, ok := parseTaskDefArn(info.TaskDefinitionArn); ok {
		info.TaskDefinitionFamily = family
		info.TaskDefinitionRevision = rev
	}
	return info, nil
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

// parseTaskDefArn extracts family and revision from a task definition ARN of
// the form "arn:aws:ecs:<region>:<account>:task-definition/<family>:<revision>".
func parseTaskDefArn(arn string) (family string, revision int64, ok bool) {
	slash := strings.LastIndex(arn, "/")
	if slash < 0 {
		return "", 0, false
	}
	tail := arn[slash+1:]
	colon := strings.LastIndex(tail, ":")
	if colon < 0 {
		return "", 0, false
	}
	rev, err := strconv.ParseInt(tail[colon+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return tail[:colon], rev, true
}
