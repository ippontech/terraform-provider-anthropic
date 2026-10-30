// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                     = &PluginResource{}
	_ resource.ResourceWithImportState      = &PluginResource{}
	_ resource.ResourceWithModifyPlan       = &PluginResource{}
	_ resource.ResourceWithConfigValidators = &PluginResource{}
)

func NewPluginResource() resource.Resource {
	return &PluginResource{}
}

// PluginResource manages an organization-owned plugin: it uploads the plugin
// and its first version, chooses the version members are served, and deletes
// the plugin (with every version it holds) on destroy. Later content is
// published with anthropic_plugin_version.
type PluginResource struct {
	client *admin.Client
}

type PluginResourceModel struct {
	ID                                          types.String `tfsdk:"id"`
	SourceDir                                   types.String `tfsdk:"source_dir"`
	SourceArchive                               types.String `tfsdk:"source_archive"`
	MarketplaceID                               types.String `tfsdk:"marketplace_id"`
	ReleaseNotes                                types.String `tfsdk:"release_notes"`
	ServedVersionID                             types.String `tfsdk:"served_version_id"`
	ServedVersionPinned                         types.Bool   `tfsdk:"served_version_pinned"`
	LatestVersionID                             types.String `tfsdk:"latest_version_id"`
	Name                                        types.String `tfsdk:"name"`
	DisplayName                                 types.String `tfsdk:"display_name"`
	Description                                 types.String `tfsdk:"description"`
	ManifestVersion                             types.String `tfsdk:"manifest_version"`
	OrganizationInstallationPreference          types.String `tfsdk:"organization_installation_preference"`
	OrganizationInstallationPreferenceInherited types.Bool   `tfsdk:"organization_installation_preference_inherited"`
	ContentScan                                 types.Object `tfsdk:"content_scan"`
	Components                                  types.List   `tfsdk:"components"`
	Reach                                       types.String `tfsdk:"reach"`
	CreatedBy                                   types.Object `tfsdk:"created_by"`
	CreatedAt                                   types.String `tfsdk:"created_at"`
	UpdatedAt                                   types.String `tfsdk:"updated_at"`
}

func (r *PluginResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}

// createOnlyNote is appended to the description of the inputs read only when
// the plugin is created.
const createOnlyNote = " Read only when the plugin is created: a later change is recorded in state but uploads nothing " +
	"(Terraform shows a warning). Publish new content with `anthropic_plugin_version`."

// served_version_id must name a version of this plugin (the API answers 404
// otherwise), and a plugin about to be created has none yet: whatever it is
// set to then belongs to another plugin, typically the one being replaced.
const (
	servedVersionOnCreateSummary = "served_version_id set on a new plugin"
	servedVersionOnCreateDetail  = "`served_version_id` must name a version of this plugin, and a plugin about to be created " +
		"has none yet. This also happens when the plugin is replaced (a `marketplace_id` change, `-replace`) or was deleted " +
		"outside Terraform: the configured ID belongs to the previous plugin. Remove `served_version_id`, apply to create " +
		"the plugin, then set it to one of the new plugin's versions."
)

func (r *PluginResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Uploads an organization-owned plugin to a Claude Enterprise organization (Plugins API, beta), " +
			"chooses the version members are served, and deletes the plugin with every version it holds on destroy. " +
			"The plugin's first version comes from `source_dir` or `source_archive`; publish later versions with " +
			"`anthropic_plugin_version`. Requires a Claude Enterprise Admin API key with the `read:plugins` and " +
			"`write:plugins` scopes in `admin_api_key`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The plugin's ID (prefixed `plugin_`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"source_dir": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Local directory holding the plugin, with its manifest at `.claude-plugin/plugin.json`. " +
					"Every regular file under it is uploaded under its path within the directory, except Git metadata " +
					"(`.git`, at any depth), so point it at the plugin's root (a Git checkout of the plugin alone is fine); " +
					"symbolic links are refused. Conflicts with " +
					"`source_archive`; one of them is required to create a plugin." + createOnlyNote,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"source_archive": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Local `.zip` or `.plugin` archive holding the plugin (it may wrap everything in one " +
					"top-level folder). Conflicts with `source_dir`." + createOnlyNote,
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(pluginArchivePattern), "must name a .zip or .plugin archive")},
			},
			"marketplace_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID of the organization-owned `manual` marketplace to create the plugin in " +
					"(prefixed `marketplace_`). Defaults to the organization's library marketplace, which the first " +
					"upload creates. Changing it forces a new plugin.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"release_notes": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Release notes of the first version, shown in its version history in claude.ai (up to 5,000 characters)." + createOnlyNote,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(5000)},
			},
			"served_version_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "The version members are served (prefixed `pluginver_`). Left unset, it follows " +
					"the newest version until the plugin is pinned. Setting it **pins** the plugin: later uploads are " +
					"stored but not served until this value names them, and a pinned plugin cannot be unpinned (removing " +
					"the attribute leaves the pin where it is). Set an earlier version to roll back, a newer one to " +
					"promote it. Because `anthropic_plugin_version` refers to its plugin, a version resource cannot be " +
					"referenced here (that would be a dependency cycle): pass the version ID in, from a variable for " +
					"example. Set it only once the plugin exists: a plan that creates the plugin, a replacement included, " +
					"fails while it is set. With content scanning on, a version whose scan is still running or failed is refused.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^pluginver_`), "must be a plugin version ID (prefixed pluginver_)")},
			},
			"served_version_pinned": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether the served version is pinned: `false` while it follows the newest " +
					"version, `true` once a version was chosen (here or in claude.ai), for good.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"latest_version_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The newest version (prefixed `pluginver_`), which differs from `served_version_id` while a stored version is not served.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The manifest's `name`: a lowercase identifier, unique within the marketplace and fixed for the plugin's lifetime.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The served version's manifest `displayName`, or null.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The served version's manifest `description`, or null.",
			},
			"manifest_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The served version's manifest `version`, normalized for display (a leading `v` dropped), or null.",
			},
			"organization_installation_preference": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The organization-wide installation setting: the plugin's own, or its marketplace's " +
					"default (`required`, `auto_install`, `available` or `not_available`).",
			},
			"organization_installation_preference_inherited": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether `organization_installation_preference` comes from the marketplace's default.",
			},
			"content_scan": contentScanResourceAttribute("The served version's content scan, null when it was never scanned."),
			"components":   componentsResourceAttribute("The served version's components, null when not recorded."),
			"reach":        reachResourceAttribute("served version"),
			"created_by":   createdByResourceAttribute("Who created the plugin, null when no creator is recorded."),
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the plugin's creation.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the plugin's last served-version change or new version (installation settings do not move it).",
			},
		},
	}
}

func (r *PluginResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.Conflicting(path.MatchRoot("source_dir"), path.MatchRoot("source_archive")),
	}
}

func (r *PluginResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan does three things the attribute plan modifiers cannot:
//   - a create needs a source, but an imported plugin does not, so the
//     requirement is checked here rather than with ExactlyOneOf; a create
//     (a replacement included) cannot take served_version_id, which can
//     only name a version of an existing plugin;
//   - configuring served_version_id pins the plugin, so the plan says so: an
//     unpinned plugin whose served version already matches the configuration
//     then still gets the update that pins it, with the values that update
//     returns left unknown. The pin itself is planned unknown while
//     served_version_id is, and when it names the version an unpinned plugin
//     already serves: with ignore_changes, that value is the prior one that
//     Terraform puts in the plan's configuration but not in the apply's, and
//     Update pins only for the configuration it applies;
//   - a change to an input read only at creation is a no-op, which a warning
//     says.
func (r *PluginResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var config PluginResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pinMayComeFromIgnoreChanges := false
	if req.State.Raw.IsNull() {
		resp.Diagnostics.Append(validatePluginCreateConfig(config)...)
	} else {
		var state PluginResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(warnUploadInputChanges(config, state)...)
		// Pinning moves updated_at, even onto the version already served
		// ("including pinning an unpinned Plugin to the version it already
		// serves", Update Plugin), and Update maps the whole answer of the
		// serve. When no configured value changed, the framework plans every
		// computed value from state; they are left unknown here, as the
		// framework does for any other change, since the plugin may change
		// between plan and apply.
		if !config.ServedVersionID.IsNull() && !state.ServedVersionPinned.ValueBool() {
			resp.Diagnostics.Append(planServeResultUnknown(ctx, &resp.Plan)...)
			pinMayComeFromIgnoreChanges = config.ServedVersionID.Equal(state.ServedVersionID)
		}
	}

	switch {
	case config.ServedVersionID.IsUnknown(), pinMayComeFromIgnoreChanges:
		// An unknown value may still turn out null, which pins nothing; the
		// version an unpinned plugin serves may be ignore_changes' prior value,
		// absent from the configuration Update applies. A pin planned now would
		// then not be made.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("served_version_pinned"), types.BoolUnknown())...)
	case !config.ServedVersionID.IsNull():
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("served_version_pinned"), true)...)
	}
}

// validatePluginCreateConfig checks the configuration of a plugin about to be
// created: its first version is uploaded from source_dir or source_archive,
// and served_version_id cannot name a version yet. A served_version_id
// unknown here may still turn out null: Create checks it.
func validatePluginCreateConfig(config PluginResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if config.SourceDir.IsNull() && config.SourceArchive.IsNull() {
		diags.AddAttributeError(path.Root("source_dir"), "Missing plugin source",
			"Creating a plugin uploads its first version: set `source_dir` or `source_archive`.")
	}
	if knownString(config.ServedVersionID) != "" {
		diags.AddAttributeError(path.Root("served_version_id"), servedVersionOnCreateSummary, servedVersionOnCreateDetail)
	}
	return diags
}

// warnUploadInputChanges warns about a change to an input read only when the
// plugin is created, since it uploads nothing.
func warnUploadInputChanges(config, state PluginResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, input := range []struct {
		name          string
		config, state types.String
	}{
		{"source_dir", config.SourceDir, state.SourceDir},
		{"source_archive", config.SourceArchive, state.SourceArchive},
		{"release_notes", config.ReleaseNotes, state.ReleaseNotes},
	} {
		// A null state value is an imported plugin, or an input set for the
		// first time: nothing was uploaded from it, so nothing to warn about.
		if !input.state.IsNull() && !input.config.Equal(input.state) {
			diags.AddAttributeWarning(path.Root(input.name), "Change uploads nothing",
				fmt.Sprintf("`%s` is only read when the plugin is created. This change is recorded in state, but "+
					"the plugin keeps its content: publish new content with an `anthropic_plugin_version` resource.", input.name))
		}
	}
	return diags
}

// planServeResultUnknown leaves unknown in the plan every computed attribute
// that Update takes from the serve's answer and that has no
// UseStateForUnknown.
func planServeResultUnknown(ctx context.Context, plan *tfsdk.Plan) diag.Diagnostics {
	var diags diag.Diagnostics
	for name, unknown := range map[string]attr.Value{
		"latest_version_id":                    types.StringUnknown(),
		"display_name":                         types.StringUnknown(),
		"description":                          types.StringUnknown(),
		"manifest_version":                     types.StringUnknown(),
		"organization_installation_preference": types.StringUnknown(),
		"organization_installation_preference_inherited": types.BoolUnknown(),
		"content_scan": types.ObjectUnknown(pluginContentScanAttrTypes),
		"components":   types.ListUnknown(types.ObjectType{AttrTypes: pluginComponentAttrTypes}),
		"reach":        types.StringUnknown(),
		"created_by":   types.ObjectUnknown(pluginActorAttrTypes),
		"updated_at":   types.StringUnknown(),
	} {
		diags.Append(plan.SetAttribute(ctx, path.Root(name), unknown)...)
	}
	return diags
}

func (r *PluginResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PluginResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Checked before anything is uploaded, for a value ModifyPlan could not
	// see: a tainted plugin would be deleted and uploaded again by every
	// later apply.
	if knownString(data.ServedVersionID) != "" {
		resp.Diagnostics.AddAttributeError(path.Root("served_version_id"), servedVersionOnCreateSummary, servedVersionOnCreateDetail)
		return
	}

	src, err := listPluginSource(data.SourceDir.ValueString(), data.SourceArchive.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid plugin source", err.Error())
		return
	}

	plugin, err := createPlugin(ctx, r.client, src, knownString(data.MarketplaceID), data.ReleaseNotes.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create plugin: %s", err))
		return
	}

	resp.Diagnostics.Append(mapPluginToState(plugin, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PluginResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plugin, err := getPlugin(ctx, r.client, data.ID.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin: %s", err))
		return
	}

	resp.Diagnostics.Append(mapPluginToState(plugin, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update changes the served version when the configuration asks for another
// one, or for a pin. Any other in-place change is to an input read only at
// creation: the prior state is kept, with the new input values recorded.
func (r *PluginResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var config, plan, state PluginResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result := state
	if want := plan.ServedVersionID.ValueString(); !config.ServedVersionID.IsNull() &&
		(want != state.ServedVersionID.ValueString() || !state.ServedVersionPinned.ValueBool()) {
		served, err := servePluginVersion(ctx, r.client, state.ID.ValueString(), want)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to serve plugin version %s: %s", want, err))
			return
		}
		resp.Diagnostics.Append(mapPluginToState(served, &result)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	result.SourceDir = plan.SourceDir
	result.SourceArchive = plan.SourceArchive
	result.ReleaseNotes = plan.ReleaseNotes
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *PluginResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PluginResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := deletePlugin(ctx, r.client, data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete plugin: %s", err))
	}
}

func (r *PluginResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// knownString returns a known value's string, and "" for null or unknown.
func knownString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// mapPluginToState maps a plugin onto the resource model, leaving the inputs
// read only at creation (source_dir, source_archive, release_notes) alone: the
// API does not return them.
func mapPluginToState(p *pluginAPIObject, data *PluginResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(p.ID)
	data.MarketplaceID = types.StringValue(p.MarketplaceID)
	data.ServedVersionID = tfvalue.StringOrNull(p.ServedVersionID)
	data.ServedVersionPinned = types.BoolValue(p.ServedVersionPinned)
	data.LatestVersionID = tfvalue.StringOrNull(p.LatestVersionID)
	data.Name = types.StringValue(p.Name)
	data.DisplayName = tfvalue.StringOrNull(p.DisplayName)
	data.Description = tfvalue.StringOrNull(p.Description)
	data.ManifestVersion = tfvalue.StringOrNull(p.ManifestVersion)
	data.OrganizationInstallationPreference = tfvalue.StringOrNull(p.OrganizationInstallationPreference)
	data.OrganizationInstallationPreferenceInherited = types.BoolPointerValue(p.OrganizationInstallationPreferenceInherited)
	data.Reach = tfvalue.StringOrNull(p.Reach)
	data.CreatedAt = types.StringValue(p.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(p.UpdatedAt)

	var d diag.Diagnostics
	data.ContentScan, d = contentScanValue(p.ContentScan)
	diags.Append(d...)
	data.Components, d = componentsValue(p.Components)
	diags.Append(d...)
	data.CreatedBy, d = actorValue(p.CreatedBy)
	diags.Append(d...)

	return diags
}

// --- Computed attributes shared by anthropic_plugin and anthropic_plugin_version ---

func contentScanResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description + " Members are served a version only once its scan is `completed` with `pass` or `warn`.",
		Attributes: map[string]schema.Attribute{
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`processing` while the scan runs, `completed` when it finished, `errored` when it could not.",
			},
			"assessment": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Set once `status` is `completed`: `pass`, `warn`, `fail` or `unknown`.",
			},
			"reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "For `warn` and `fail`, the main concern (for example `credential-exposure`), otherwise null.",
			},
		},
	}
}

func componentsResourceAttribute(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"type": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: "`skill`, `mcp_server`, `command`, `agent`, `hook` or `cli`.",
				},
				"name": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: "The component's name: an MCP server's key in the manifest, a hook's event, a CLI's executable.",
				},
				"description": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: "What the component declares about itself; always null for MCP servers, hooks and CLIs.",
				},
			},
		},
	}
}

func reachResourceAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed: true,
		MarkdownDescription: "How far the " + what + " reaches: `remote` (declares an MCP server or a CLI), `privileged` " +
			"(runs or takes effect on members' machines: hooks, settings, pre-approved tools) or `contained`; null when unclassified.",
	}
}

func createdByResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`user_actor` (a member in claude.ai) or `api_actor` (an Admin API key); other types may appear.",
			},
			"user_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The member's user ID, for a `user_actor`.",
			},
			"email_address": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The member's email address, for a `user_actor` (may be null).",
			},
			"api_key_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The Admin API key's ID, for an `api_actor`.",
			},
		},
	}
}
