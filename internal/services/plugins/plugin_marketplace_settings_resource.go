// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &PluginMarketplaceSettingsResource{}
	_ resource.ResourceWithImportState = &PluginMarketplaceSettingsResource{}
)

func NewPluginMarketplaceSettingsResource() resource.Resource {
	return &PluginMarketplaceSettingsResource{}
}

// PluginMarketplaceSettingsResource manages the one setting the API can write
// on a marketplace: its default installation setting. Marketplaces are
// created, connected to a repository and deleted in claude.ai, so Create
// adopts an existing marketplace and Delete only forgets it.
type PluginMarketplaceSettingsResource struct {
	client *admin.Client
}

type PluginMarketplaceSettingsResourceModel struct {
	ID                            types.String `tfsdk:"id"`
	MarketplaceID                 types.String `tfsdk:"marketplace_id"`
	DefaultInstallationPreference types.String `tfsdk:"default_installation_preference"`
	Name                          types.String `tfsdk:"name"`
	Source                        types.String `tfsdk:"source"`
}

func (r *PluginMarketplaceSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_marketplace_settings"
}

func (r *PluginMarketplaceSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the default installation setting of one of a Claude Enterprise organization's " +
			"plugin marketplaces (Plugins API, beta): the organization-wide value of every plugin in it without a " +
			"setting of its own. The marketplace itself is created and deleted in claude.ai: creating this resource " +
			"adopts an existing marketplace, and destroying it leaves the marketplace and its default as they are. " +
			"Requires a Claude Enterprise Admin API key with the `read:plugins` and `write:plugins` scopes in `admin_api_key`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The marketplace's ID, same as `marketplace_id`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"marketplace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of one of the organization's marketplaces (prefixed `marketplace_`), `manual` or synchronized from Git.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^marketplace_`), "must be a marketplace ID (prefixed marketplace_)")},
			},
			"default_installation_preference": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The organization-wide installation setting of every plugin in the marketplace " +
					"without one of its own, including plugins added later: `required`, `auto_install`, `available` or " +
					"`not_available`. Once set, a default can be changed but never removed.",
				Validators: []validator.String{stringvalidator.OneOf(installationPreferences...)},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The marketplace's name, fixed for its lifetime.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"source": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Where its plugins come from: `manual` (uploaded), or `github`, `gitlab`, `public_git` (synchronized from a repository).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *PluginMarketplaceSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create adopts the marketplace and sets its default: there is nothing to
// create, and the write is safe to repeat.
func (r *PluginMarketplaceSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PluginMarketplaceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marketplace, err := setMarketplaceDefault(ctx, r.client, data.MarketplaceID.ValueString(), data.DefaultInstallationPreference.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to set the marketplace's default installation setting: %s", err))
		return
	}

	mapMarketplaceSettingsToState(marketplace, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginMarketplaceSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PluginMarketplaceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marketplace, err := getPluginMarketplace(ctx, r.client, data.MarketplaceID.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin marketplace: %s", err))
		return
	}

	mapMarketplaceSettingsToState(marketplace, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginMarketplaceSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data PluginMarketplaceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marketplace, err := setMarketplaceDefault(ctx, r.client, data.MarketplaceID.ValueString(), data.DefaultInstallationPreference.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to set the marketplace's default installation setting: %s", err))
		return
	}

	mapMarketplaceSettingsToState(marketplace, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete only forgets the marketplace: a default cannot be set back to null,
// and removing a marketplace has no API.
func (r *PluginMarketplaceSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PluginMarketplaceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg := fmt.Sprintf("Marketplace %s keeps %q as its default installation setting: the API cannot remove a default, "+
		"and marketplaces are removed in claude.ai. The settings were removed from Terraform state only.",
		data.MarketplaceID.ValueString(), data.DefaultInstallationPreference.ValueString())
	resp.Diagnostics.AddWarning("Marketplace Settings Not Reset", msg)
	tflog.Warn(ctx, msg)
}

func (r *PluginMarketplaceSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("marketplace_id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

func mapMarketplaceSettingsToState(m *pluginMarketplaceAPIObject, data *PluginMarketplaceSettingsResourceModel) {
	data.ID = types.StringValue(m.ID)
	data.MarketplaceID = types.StringValue(m.ID)
	data.DefaultInstallationPreference = tfvalue.StringOrNull(m.DefaultInstallationPreference)
	data.Name = types.StringValue(m.Name)
	data.Source = types.StringValue(m.Source)
}
