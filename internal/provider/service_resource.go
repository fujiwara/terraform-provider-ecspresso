package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = (*serviceResource)(nil)
	_ resource.ResourceWithImportState = (*serviceResource)(nil)
)

type serviceResource struct{}

func NewServiceResource() resource.Resource {
	return &serviceResource{}
}

// serviceResourceModel is the in-memory representation of an
// ecspresso_service resource. Field tags must match the schema attribute
// names exactly.
type serviceResourceModel struct {
	ID                types.String  `tfsdk:"id"`
	ConfigPath        types.String  `tfsdk:"config_path"`
	TFStateValues     types.Dynamic `tfsdk:"tfstate_values"`
	TFStateFuncPrefix types.String  `tfsdk:"tfstate_func_prefix"`
	DestroyAction     types.String  `tfsdk:"destroy_action"`
	ServiceArn        types.String  `tfsdk:"service_arn"`
	ServiceName       types.String  `tfsdk:"service_name"`
	ClusterArn        types.String  `tfsdk:"cluster_arn"`
	ClusterName       types.String  `tfsdk:"cluster_name"`
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
			"tfstate_values": schema.DynamicAttribute{
				Description: "Object whose keys are tfstate addresses at the resource level (e.g. `\"aws_iam_role.task\"`, `\"output.foo\"`). Each value may be any Terraform type — a whole resource attribute map, a list, a bool, or a scalar — and the corresponding ecspresso jsonnet/template lookups, including nested ones like `tfstate('aws_iam_role.task.arn')`, are resolved against it. Overrides take precedence over the tfstate file the plugin loads from `path` / `url`. A diff in this attribute is the primary signal that causes Terraform to re-run an ecspresso deploy. Declared as a Dynamic attribute (not a typed map) because Plugin Framework does not support Dynamic element types inside collections.",
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

	tfstateOverrides := tfstateOverridesFromPlan(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString(), plan.TFStateFuncPrefix.ValueString(), tfstateOverrides)
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	// Carry the planned raw value (sensitivity markers and all) straight into
	// state, then overwrite only the computed attributes. Round-tripping
	// through serviceResourceModel for a Dynamic attribute drops nested
	// sensitivity marks, which trips Terraform's "inconsistent values for
	// sensitive attribute" check at apply time.
	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
}

func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := ecspressoapi.Describe(ctx, state.ConfigPath.ValueString())
	if err != nil {
		if ecspressoapi.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("ecspresso describe failed", err.Error())
		return
	}

	resp.State.Raw = req.State.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tfstateOverrides := tfstateOverridesFromPlan(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString(), plan.TFStateFuncPrefix.ValueString(), tfstateOverrides)
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(setComputedFromInfo(ctx, &resp.State, info)...)
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

	tfstateOverrides := tfstateOverridesFromPlan(ctx, state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := ecspressoapi.Delete(ctx, state.ConfigPath.ValueString(), state.TFStateFuncPrefix.ValueString(), tfstateOverrides); err != nil {
		resp.Diagnostics.AddError("ecspresso delete failed", err.Error())
		return
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// tfstateOverridesFromPlan extracts the tfstate_values attribute as a
// native map[string]any suitable for ecspresso.App.SetTFStateOverrides.
// The attribute is declared as a Dynamic and the underlying value is
// expected to be an Object literal in HCL; a typed Map is also accepted
// for symmetry. Null / unknown yield nil.
func tfstateOverridesFromPlan(_ context.Context, m serviceResourceModel, diags *diag.Diagnostics) map[string]any {
	if m.TFStateValues.IsNull() || m.TFStateValues.IsUnknown() {
		return nil
	}
	underlying := m.TFStateValues.UnderlyingValue()
	var elems map[string]attr.Value
	switch u := underlying.(type) {
	case types.Object:
		elems = u.Attributes()
	case types.Map:
		elems = u.Elements()
	default:
		diags.AddError(
			"unsupported tfstate_values shape",
			fmt.Sprintf("tfstate_values must be an object or map, got %T", underlying),
		)
		return nil
	}
	out := make(map[string]any, len(elems))
	for k, v := range elems {
		goVal, err := attrValueToGo(v)
		if err != nil {
			diags.AddError(
				"failed to extract tfstate_values element",
				fmt.Sprintf("tfstate_values[%q]: %s", k, err),
			)
			continue
		}
		out[k] = goVal
	}
	return out
}

// attrValueToGo materializes an attr.Value as a generic Go value suitable
// for ecspresso.App.SetTFStateOverrides (i.e. the same shape json.Unmarshal
// into any would produce). Null becomes Go nil. Unknown values are rejected
// — callers should not reach this path with unknown plan values.
func attrValueToGo(v attr.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}
	if v.IsUnknown() {
		return nil, fmt.Errorf("value is unknown at apply time")
	}
	switch c := v.(type) {
	case types.String:
		return c.ValueString(), nil
	case types.Bool:
		return c.ValueBool(), nil
	case types.Int64:
		return c.ValueInt64(), nil
	case types.Float64:
		return c.ValueFloat64(), nil
	case types.Number:
		// gojq (used downstream by tfstate-lookup) expects float64 for
		// numeric values. Precision loss is possible for very large
		// integers; tfstate addresses in practice are not numeric so
		// this is acceptable.
		f, _ := c.ValueBigFloat().Float64()
		return f, nil
	case types.List:
		return attrValueSlice(c.Elements())
	case types.Set:
		return attrValueSlice(c.Elements())
	case types.Tuple:
		return attrValueSlice(c.Elements())
	case types.Map:
		return attrValueMap(c.Elements())
	case types.Object:
		return attrValueMap(c.Attributes())
	case types.Dynamic:
		return attrValueToGo(c.UnderlyingValue())
	}
	return nil, fmt.Errorf("unsupported attr.Value type %T", v)
}

func attrValueSlice(elems []attr.Value) ([]any, error) {
	out := make([]any, len(elems))
	for i, e := range elems {
		sub, err := attrValueToGo(e)
		if err != nil {
			return nil, err
		}
		out[i] = sub
	}
	return out, nil
}

func attrValueMap(elems map[string]attr.Value) (map[string]any, error) {
	out := make(map[string]any, len(elems))
	for k, e := range elems {
		sub, err := attrValueToGo(e)
		if err != nil {
			return nil, err
		}
		out[k] = sub
	}
	return out, nil
}

// setComputedFromInfo writes the eight Computed attributes directly into
// resp.State via SetAttribute. This preserves the surrounding raw state
// (and its sensitivity markers on tfstate_values) that the caller already
// copied from req.Plan / req.State.
func setComputedFromInfo(ctx context.Context, state *tfsdk.State, info *ecspressoapi.ServiceInfo) diag.Diagnostics {
	var diags diag.Diagnostics
	id := info.ClusterName + "/" + info.ServiceName
	diags.Append(state.SetAttribute(ctx, path.Root("id"), id)...)
	diags.Append(state.SetAttribute(ctx, path.Root("service_arn"), info.ServiceArn)...)
	diags.Append(state.SetAttribute(ctx, path.Root("service_name"), info.ServiceName)...)
	diags.Append(state.SetAttribute(ctx, path.Root("cluster_arn"), info.ClusterArn)...)
	diags.Append(state.SetAttribute(ctx, path.Root("cluster_name"), info.ClusterName)...)
	return diags
}
