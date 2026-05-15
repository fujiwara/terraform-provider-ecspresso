package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	ID                     types.String `tfsdk:"id"`
	ConfigPath             types.String `tfsdk:"config_path"`
	TFStateValues          types.Map    `tfsdk:"tfstate_values"`
	DestroyAction          types.String `tfsdk:"destroy_action"`
	ServiceArn             types.String `tfsdk:"service_arn"`
	ServiceName            types.String `tfsdk:"service_name"`
	ClusterArn             types.String `tfsdk:"cluster_arn"`
	ClusterName            types.String `tfsdk:"cluster_name"`
	TaskDefinitionArn      types.String `tfsdk:"task_definition_arn"`
	TaskDefinitionFamily   types.String `tfsdk:"task_definition_family"`
	TaskDefinitionRevision types.Int64  `tfsdk:"task_definition_revision"`
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
				Description: "Path to the ecspresso configuration file (typically `ecspresso.yml`). Changing this forces a new resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tfstate_values": schema.MapAttribute{
				Description: "Values injected into ecspresso's tfstate plugin. Keys are the tfstate addresses referenced from `ecspresso.yml`. Requires `from_provider: true` on the tfstate plugin in the ecspresso config. (Not yet wired in this version — accepted by the schema but ignored.)",
				Optional:    true,
				ElementType: types.StringType,
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
			"task_definition_arn": schema.StringAttribute{
				Description: "ARN of the task definition the service is currently running.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"task_definition_family": schema.StringAttribute{
				Description: "Family of the task definition the service is currently running.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"task_definition_revision": schema.Int64Attribute{
				Description: "Revision of the task definition the service is currently running.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	applyServiceInfo(&plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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

	applyServiceInfo(&state, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serviceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := ecspressoapi.Deploy(ctx, plan.ConfigPath.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("ecspresso deploy failed", err.Error())
		return
	}

	applyServiceInfo(&plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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

	if err := ecspressoapi.Delete(ctx, state.ConfigPath.ValueString()); err != nil {
		resp.Diagnostics.AddError("ecspresso delete failed", err.Error())
		return
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// applyServiceInfo writes computed attribute values back into the model
// after a Deploy or Describe call. The model's input fields (config_path,
// tfstate_values, destroy_action) are not touched.
func applyServiceInfo(m *serviceResourceModel, info *ecspressoapi.ServiceInfo) {
	m.ID = types.StringValue(info.ClusterName + "/" + info.ServiceName)
	m.ServiceArn = types.StringValue(info.ServiceArn)
	m.ServiceName = types.StringValue(info.ServiceName)
	m.ClusterArn = types.StringValue(info.ClusterArn)
	m.ClusterName = types.StringValue(info.ClusterName)
	m.TaskDefinitionArn = types.StringValue(info.TaskDefinitionArn)
	m.TaskDefinitionFamily = types.StringValue(info.TaskDefinitionFamily)
	m.TaskDefinitionRevision = types.Int64Value(info.TaskDefinitionRevision)
}
