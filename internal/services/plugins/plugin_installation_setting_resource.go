// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &PluginInstallationSettingResource{}
	_ resource.ResourceWithImportState = &PluginInstallationSettingResource{}
)

func NewPluginInstallationSettingResource() resource.Resource {
	return &PluginInstallationSettingResource{}
}

// PluginInstallationSettingResource manages the installation setting an
// organization-owned plugin holds for one target: the whole organization, or
// one RBAC group.
type PluginInstallationSettingResource struct {
	client *admin.Client
}

type PluginInstallationSettingResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	PluginID               types.String `tfsdk:"plugin_id"`
	Target                 types.String `tfsdk:"target"`
	InstallationPreference types.String `tfsdk:"installation_preference"`
	TargetType             types.String `tfsdk:"target_type"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

// installationSettingTargetPattern matches the targets the API accepts: the
// literal `organization`, or an RBAC group's ID. Anything else is a 400. A
// colon is refused too: the resource ID joins the plugin and the target with
// one.
var installationSettingTargetPattern = regexp.MustCompile(`^(organization|rbac_group_[^:/\s]+)$`)

func (r *PluginInstallationSettingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_installation_setting"
}

func (r *PluginInstallationSettingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages who can use an organization-owned plugin in a Claude Enterprise organization " +
			"(Plugins API, beta): the installation setting the plugin holds for the whole organization or for one " +
			"RBAC group. Requires a Claude Enterprise Admin API key with the `read:plugins` and `write:plugins` " +
			"scopes in `admin_api_key`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<plugin_id>:<target>`: a setting has no ID of its own.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"plugin_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the organization-owned plugin (prefixed `plugin_`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^plugin_`), "must be a plugin ID (prefixed plugin_)")},
			},
			"target": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Whose setting this is: `organization` for the plugin's organization-wide setting, " +
					"or an RBAC group's ID (prefixed `rbac_group_`) for that group's own setting.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(installationSettingTargetPattern,
					"must be `organization` or an RBAC group ID (prefixed rbac_group_)")},
			},
			"installation_preference": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "What the target's members get: `required` (installed, cannot be removed), " +
					"`auto_install` (installed, can be removed), `available` (installed on request) or `not_available` (hidden).",
				Validators: []validator.String{stringvalidator.OneOf(installationPreferences...)},
			},
			"target_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`organization` or `rbac_group`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the target was first given a setting for this plugin.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the setting's last change.",
			},
		},
	}
}

func (r *PluginInstallationSettingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireAdminResourceClient(pd.AdminClient, &resp.Diagnostics) {
		return
	}

	r.client = pd.AdminClient
}

// Create sets the target's value. A setting the target already holds (one set
// in claude.ai, say) is overwritten and adopted: the write is an upsert.
func (r *PluginInstallationSettingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &data, "create")...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginInstallationSettingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	setting, err := findPluginInstallationSetting(ctx, r.client, data.PluginID.ValueString(), data.Target.ValueString())
	if err != nil {
		// A 404 is a deleted plugin, which takes its settings with it.
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin installation setting: %s", err))
		return
	}
	if setting == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	mapInstallationSettingToState(setting, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginInstallationSettingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data PluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &data, "update")...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// write sends the planned setting and maps the answer onto data: Create and
// Update are the same upsert.
func (r *PluginInstallationSettingResource) write(ctx context.Context, data *PluginInstallationSettingResourceModel, verb string) diag.Diagnostics {
	var diags diag.Diagnostics
	setting, err := setPluginInstallationSetting(ctx, r.client, data.PluginID.ValueString(), data.Target.ValueString(),
		data.InstallationPreference.ValueString())
	if err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to %s plugin installation setting: %s", verb, err))
		return diags
	}
	mapInstallationSettingToState(setting, data)
	return diags
}

// Delete removes the target's own setting: an `organization` target returns
// the plugin to its marketplace's default, a group falls back to the
// organization-wide value (or to its members' other groups).
func (r *PluginInstallationSettingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := removePluginInstallationSetting(ctx, r.client, data.PluginID.ValueString(), data.Target.ValueString()); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove plugin installation setting: %s", err))
	}
}

// ImportState takes `<plugin_id>:<target>`, the resource's ID.
func (r *PluginInstallationSettingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	pluginID, target, ok := strings.Cut(req.ID, ":")
	if !ok || pluginID == "" || !installationSettingTargetPattern.MatchString(target) {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<plugin_id>:<target>`, the target being `organization` or an RBAC group ID; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("plugin_id"), pluginID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target"), target)...)
}

// ============================================================================
// Helper functions
// ============================================================================

func mapInstallationSettingToState(s *pluginInstallationSettingAPIObject, data *PluginInstallationSettingResourceModel) {
	data.PluginID = types.StringValue(s.PluginID)
	data.Target = types.StringValue(s.Target.pathSegment())
	data.ID = types.StringValue(s.PluginID + ":" + s.Target.pathSegment())
	data.InstallationPreference = types.StringValue(s.InstallationPreference)
	data.TargetType = types.StringValue(s.Target.Type)
	data.CreatedAt = tfvalue.StringOrNull(s.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(s.UpdatedAt)
}
