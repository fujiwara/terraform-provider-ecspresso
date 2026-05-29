package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/fujiwara/terraform-provider-ecspresso/internal/ecspressoapi"
)

const (
	DestroyActionDelete = "delete"
	DestroyActionIgnore = "ignore"
)

var (
	_ resource.Resource               = (*serviceResource)(nil)
	_ resource.ResourceWithModifyPlan = (*serviceResource)(nil)
)

type serviceResource struct{}

func NewServiceResource() resource.Resource {
	return &serviceResource{}
}

// serviceResourceModel is the in-memory representation of an
// ecspresso_service resource. Field tags must match the schema attribute
// names exactly.
type serviceResourceModel struct {
	ID                types.String `tfsdk:"id"`
	ConfigPath        types.String `tfsdk:"config_path"`
	TFStateValues     types.String `tfsdk:"tfstate_values"`
	TFStateFuncPrefix types.String `tfsdk:"tfstate_func_prefix"`
	DestroyAction     types.String `tfsdk:"destroy_action"`
	ServiceArn        types.String `tfsdk:"service_arn"`
	ServiceName       types.String `tfsdk:"service_name"`
	ClusterArn        types.String `tfsdk:"cluster_arn"`
	ClusterName       types.String `tfsdk:"cluster_name"`
	LastApplyAt       types.String `tfsdk:"last_apply_at"`
	EcspressoVersion  types.String `tfsdk:"ecspresso_version"`
}

func (r *serviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (r *serviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Amazon ECS service through ecspresso.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Synthetic ID in the form `<cluster>/<service>`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"config_path": schema.StringAttribute{
				Description: "Path to the ecspresso configuration file (typically `ecspresso.yml`). Relative paths are resolved against the working directory of the `terraform` process (i.e. where `terraform apply` is invoked), not the directory containing the `.tf` file. Prefer `\"${path.module}/ecspresso.yml\"` to anchor the path to the module, or use an absolute path. Changing this forces a new resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tfstate_values": schema.StringAttribute{
				Description: "A JSON object — pass it with `jsonencode({...})` — mapping tfstate addresses (`\"aws_iam_role.task\"`, `\"output.foo\"`) to values that ecspresso's `tfstate(...)` lookups resolve against, including nested lookups like `tfstate('aws_iam_role.task.arn')`. These take precedence over the tfstate file the plugin would load from `path` / `url`. A diff here is the primary signal that triggers an `ecspresso deploy`. It is a JSON string (not a typed object) so that referencing whole resource objects created in the same apply does not trip Terraform's \"inconsistent final plan\".",
				Optional:    true,
			},
			"tfstate_func_prefix": schema.StringAttribute{
				Description: "Identifies which tfstate plugin in `ecspresso.yml` receives the `tfstate_values` overrides. Matches the plugin's `func_prefix` field; defaults to the empty string, which targets the default (no-prefix) tfstate plugin. Only needed when the ecspresso config declares multiple tfstate plugins and the Terraform-managed half is not the default one.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"destroy_action": schema.StringAttribute{
				Description: "Action taken on destroy. `delete` (default) scales the service to 0, drains tasks, then deletes the service. `ignore` leaves the service untouched in AWS and only removes it from Terraform state.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(DestroyActionDelete),
				Validators: []validator.String{
					stringvalidator.OneOf(
						DestroyActionDelete,
						DestroyActionIgnore,
					),
				},
			},
			// Computed attributes mirroring AWS-side state. All use
			// UseStateForUnknown so unrelated changes do not blank
			// them at plan time.
			"service_arn": schema.StringAttribute{
				Description: "ARN of the ECS service.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"service_name": schema.StringAttribute{
				Description: "Name of the ECS service.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"cluster_arn": schema.StringAttribute{
				Description: "ARN of the ECS cluster.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"cluster_name": schema.StringAttribute{
				Description: "Name of the ECS cluster.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"last_apply_at": schema.StringAttribute{
				Description: "RFC3339 timestamp of the most recent `terraform apply` that actually invoked `ecspresso deploy` for this resource. This is the time on the Terraform side (the host where `terraform apply` ran), **not** the AWS-side deployment time — use `data \"aws_ecs_service\"` for live AWS-side state. In a `terraform plan`, a value of `(known after apply)` means the next apply may run `ecspresso deploy`; whether it actually does depends on ecspresso's diff against AWS. If the rendered definitions already match AWS, the deploy is skipped and the previous timestamp is preserved.",
				Computed:    true,
			},
			"ecspresso_version": schema.StringAttribute{
				Description: "Version of the ecspresso library this provider was built against. Refreshed on every apply, so a provider upgrade that swaps in a newer ecspresso shows up as a diff here. Changing this value never triggers a redeploy on its own.",
				Computed:    true,
			},
			// task_definition_* are intentionally not exposed. They
			// advance on every ecspresso deploy — including CLI deploys
			// outside Terraform's awareness — so any value Terraform
			// remembers in state is stale almost immediately. Surfacing
			// them would invite users to wire dependencies off a value
			// Terraform cannot keep authoritative. Use the AWS API or
			// `ecspresso status` to inspect the current task definition.
		},
	}
}

func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tfstateOverrides := tfstateOverridesFromPlan(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	info, deployed, warnings, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString(), plan.TFStateFuncPrefix.ValueString(), tfstateOverrides)
	addTFStatePrefixWarnings(&resp.Diagnostics, warnings)
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	// Carry the planned raw value (sensitivity markers and all) straight into
	// state, then overwrite only the computed attributes. Round-tripping the
	// model would drop marks (e.g. a sensitive tfstate_values), tripping
	// Terraform's "inconsistent values for sensitive attribute" check.
	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
	// last_apply_at reflects "the apply that actually ran a deploy".
	// On adoption-Create with no diff against AWS, the rendered configs
	// already match the deployed service and ecspresso deploy is
	// skipped; we record an empty string so the user can see no AWS-
	// side change happened.
	last := ""
	if deployed {
		last = nowTimestamp()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("last_apply_at"), last)...)
}

func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tfstateOverrides := tfstateOverridesFromPlan(state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	info, err := ecspressoapi.Describe(ctx, state.ConfigPath.ValueString(), state.TFStateFuncPrefix.ValueString(), tfstateOverrides)
	if err != nil {
		if ecspressoapi.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		if ecspressoapi.IsConfigLoadError(err) {
			// Config can't render from the tfstate_values in state (they lag a
			// pending config/values change). Skip the refresh and keep state
			// instead of failing the plan; the apply re-renders with the
			// planned values (and fails there if the config is truly broken).
			resp.Diagnostics.AddWarning(
				"ecspresso refresh skipped",
				"Could not render the ecspresso config from the current tfstate_values, "+
					"so this refresh was skipped and the last-known state is kept. This is "+
					"expected when the config or tfstate_values references a value that does "+
					"not exist yet (created in the same apply) or was just edited; the next "+
					"apply re-renders with the planned tfstate_values.\n\nDetails: "+err.Error(),
			)
			resp.State.Raw = req.State.Raw
			return
		}
		resp.Diagnostics.AddError("ecspresso describe failed", err.Error())
		return
	}

	resp.State.Raw = req.State.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !updateNeedsDeploy(plan, state) {
		// Only state-only attributes (e.g. destroy_action) changed; preserve
		// the existing computed values and skip the ecspresso deploy.
		info := &ecspressoapi.ServiceInfo{
			ServiceArn:  state.ServiceArn.ValueString(),
			ServiceName: state.ServiceName.ValueString(),
			ClusterArn:  state.ClusterArn.ValueString(),
			ClusterName: state.ClusterName.ValueString(),
		}
		resp.State.Raw = req.Plan.Raw
		resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("last_apply_at"), state.LastApplyAt.ValueString())...)
		return
	}

	tfstateOverrides := tfstateOverridesFromPlan(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	info, deployed, warnings, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString(), plan.TFStateFuncPrefix.ValueString(), tfstateOverrides)
	addTFStatePrefixWarnings(&resp.Diagnostics, warnings)
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
	// Update the timestamp only when ecspresso actually ran a deploy.
	// When `tfstate_values` changes but the rendered definitions still
	// match AWS (HasDiff = false), the previous timestamp survives so
	// `last_apply_at` keeps accurately reporting "when did this resource
	// last cause AWS state to change".
	if deployed {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("last_apply_at"), nowTimestamp())...)
	} else {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("last_apply_at"), state.LastApplyAt.ValueString())...)
	}
}

// ModifyPlan controls how last_apply_at shows up in plan output. On
// Update, set it to Unknown ("(known after apply)") when an ecspresso
// deploy will actually run, and carry the prior state value forward
// when the update is state-only (e.g. destroy_action change). On
// Create the attribute is naturally Unknown so no action is needed;
// on Destroy there is no plan to mutate.
func (r *serviceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var plan, state serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if updateNeedsDeploy(plan, state) {
		plan.LastApplyAt = types.StringUnknown()
	} else {
		plan.LastApplyAt = state.LastApplyAt
	}
	// ecspresso_version is known at plan time — it comes from the
	// provider binary, not from AWS. Setting it here surfaces any
	// upcoming version change as a plain attribute diff instead of
	// "(known after apply)".
	plan.EcspressoVersion = types.StringValue(ecspressoapi.Version())
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

// nowTimestamp returns the current time formatted as RFC3339 in UTC.
// Wrapped in a variable to allow tests to substitute a fixed clock if needed.
var nowTimestamp = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// updateNeedsDeploy reports whether an Update should invoke ecspresso deploy.
// destroy_action only affects Delete behaviour and must not trigger a
// redeploy. config_path is RequiresReplace so it never reaches Update.
func updateNeedsDeploy(plan, state serviceResourceModel) bool {
	return !plan.TFStateValues.Equal(state.TFStateValues) ||
		!plan.TFStateFuncPrefix.Equal(state.TFStateFuncPrefix)
}

// addTFStatePrefixWarnings surfaces the advisories ecspressoapi.Deploy
// returns (currently the tfstate_func_prefix / config mismatch) as
// Terraform warning diagnostics so a likely-misrouted tfstate_values is
// visible at apply time instead of failing silently.
func addTFStatePrefixWarnings(diags *diag.Diagnostics, warnings []string) {
	for _, w := range warnings {
		diags.AddWarning("tfstate_func_prefix may not match the ecspresso config", w)
	}
}

func (r *serviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.DestroyAction.ValueString() == DestroyActionIgnore {
		return
	}

	tfstateOverrides := tfstateOverridesFromPlan(state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := ecspressoapi.Delete(ctx, state.ConfigPath.ValueString(), state.TFStateFuncPrefix.ValueString(), tfstateOverrides); err != nil {
		resp.Diagnostics.AddError("ecspresso delete failed", err.Error())
		return
	}
}

// tfstateOverridesFromPlan decodes the tfstate_values attribute — a JSON
// object string from `jsonencode({...})` — into the map[string]any that
// ecspresso resolves `tfstate(...)` lookups against. It's a string (not a
// typed object) to dodge "inconsistent final plan" on whole-object refs;
// see the schema Description. Null / unknown yield nil.
func tfstateOverridesFromPlan(m serviceResourceModel, diags *diag.Diagnostics) map[string]any {
	if m.TFStateValues.IsNull() || m.TFStateValues.IsUnknown() {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(m.TFStateValues.ValueString()), &out); err != nil {
		diags.AddError(
			"invalid tfstate_values JSON",
			fmt.Sprintf("tfstate_values must be a JSON object (use jsonencode of an object): %s", err),
		)
		return nil
	}
	return out
}

// setComputedFromInfo writes the Computed attributes that mirror AWS-
// side state directly into resp.State via SetAttribute. It also writes
// ecspresso_version, which is sourced from the linked ecspresso library
// rather than AWS. This preserves the surrounding raw state (and its
// sensitivity markers on tfstate_values) that the caller already copied
// from req.Plan / req.State.
func setComputedFromInfo(ctx context.Context, state *tfsdk.State, info *ecspressoapi.ServiceInfo) diag.Diagnostics {
	var diags diag.Diagnostics
	id := info.ClusterName + "/" + info.ServiceName
	diags.Append(state.SetAttribute(ctx, path.Root("id"), id)...)
	diags.Append(state.SetAttribute(ctx, path.Root("service_arn"), info.ServiceArn)...)
	diags.Append(state.SetAttribute(ctx, path.Root("service_name"), info.ServiceName)...)
	diags.Append(state.SetAttribute(ctx, path.Root("cluster_arn"), info.ClusterArn)...)
	diags.Append(state.SetAttribute(ctx, path.Root("cluster_name"), info.ClusterName)...)
	diags.Append(state.SetAttribute(ctx, path.Root("ecspresso_version"), ecspressoapi.Version())...)
	return diags
}
