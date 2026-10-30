// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                     = &PluginVersionResource{}
	_ resource.ResourceWithConfigValidators = &PluginVersionResource{}
)

func NewPluginVersionResource() resource.Resource {
	return &PluginVersionResource{}
}

// PluginVersionResource uploads one version of a plugin. Versions are
// immutable and the API has no per-version delete, so every input forces a
// new version and destroying the resource only forgets it. There is no
// import: the API cannot say which local files a version came from, so an
// imported version would carry a source_hash describing content it may not
// hold.
type PluginVersionResource struct {
	client *admin.Client
}

type PluginVersionResourceModel struct {
	ID              types.String `tfsdk:"id"`
	PluginID        types.String `tfsdk:"plugin_id"`
	SourceDir       types.String `tfsdk:"source_dir"`
	SourceArchive   types.String `tfsdk:"source_archive"`
	SourceHash      types.String `tfsdk:"source_hash"`
	ReleaseNotes    types.String `tfsdk:"release_notes"`
	DisplayName     types.String `tfsdk:"display_name"`
	Description     types.String `tfsdk:"description"`
	ManifestVersion types.String `tfsdk:"manifest_version"`
	ContentScan     types.Object `tfsdk:"content_scan"`
	Components      types.List   `tfsdk:"components"`
	Reach           types.String `tfsdk:"reach"`
	CreatedBy       types.Object `tfsdk:"created_by"`
	CreatedAt       types.String `tfsdk:"created_at"`
}

func (r *PluginVersionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_version"
}

func (r *PluginVersionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Uploads a version of an organization-owned plugin in a Claude Enterprise organization " +
			"(Plugins API, beta). Versions are immutable: every input forces a new version, and so does any change to " +
			"the uploaded files, whose hash is recomputed at every plan. The API cannot delete a single version, so " +
			"destroying this resource only removes it from state. Import is not supported. Requires a Claude Enterprise Admin API key with the " +
			"`read:plugins` and `write:plugins` scopes in `admin_api_key`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The version's ID (prefixed `pluginver_`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"plugin_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the organization-owned plugin, in a `manual` marketplace, to add the version to (prefixed `plugin_`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^plugin_`), "must be a plugin ID (prefixed plugin_)")},
			},
			"source_dir": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Local directory holding the plugin, with its manifest at `.claude-plugin/plugin.json` " +
					"declaring the plugin's `name`. Every regular file under it is uploaded under its path within the " +
					"directory, except Git metadata (`.git`, at any depth), which `source_hash` leaves out too; symbolic " +
					"links are refused. Exactly one of `source_dir` and `source_archive` is required.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"source_archive": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Local `.zip` or `.plugin` archive holding the plugin.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(pluginArchivePattern), "must name a .zip or .plugin archive")},
			},
			"source_hash": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "SHA-256 of the uploaded content, recomputed at every plan so that an edit to the " +
					"files forces a new version: for an archive, the hash of its bytes (as `filesha256()` gives it); for " +
					"a directory, a hash over every file's path and content.",
				PlanModifiers: []planmodifier.String{pluginSourceHashModifier{}, stringplanmodifier.RequiresReplace()},
			},
			"release_notes": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Release notes, shown in the version history in claude.ai (up to 5,000 characters).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(5000)},
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The manifest's `displayName`, or null.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The manifest's `description`, or null.",
			},
			"manifest_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The manifest's `version`, normalized for display (a leading `v` dropped), or null. Not unique.",
			},
			"content_scan": contentScanResourceAttribute("The version's content scan, null when it was not scanned. " +
				"An upload starts `processing`; the verdict arrives later and shows on the next refresh."),
			"components": componentsResourceAttribute("The version's components, null when not recorded."),
			"reach":      reachResourceAttribute("version"),
			"created_by": createdByResourceAttribute("Who uploaded the version, null when not recorded."),
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the upload.",
			},
		},
	}
}

func (r *PluginVersionResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("source_dir"), path.MatchRoot("source_archive")),
	}
}

func (r *PluginVersionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PluginVersionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PluginVersionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	src, err := listPluginSource(data.SourceDir.ValueString(), data.SourceArchive.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid plugin source", err.Error())
		return
	}

	var fields []formField
	if notes := data.ReleaseNotes.ValueString(); notes != "" {
		fields = append(fields, formField{name: "release_notes", value: notes})
	}
	body, hash, err := src.uploadBody(fields...)
	if err != nil {
		resp.Diagnostics.AddError("Invalid plugin source", err.Error())
		return
	}
	// The upload is not idempotent and cannot be undone: refuse to send
	// content other than what the plan showed.
	if planned := knownString(data.SourceHash); planned != "" && planned != hash {
		resp.Diagnostics.AddError("Plugin source changed since the plan",
			fmt.Sprintf("The files to upload hash to %s, but the plan was made with %s. Run terraform plan again.", hash, planned))
		return
	}

	version, err := postPluginVersion(ctx, r.client, data.PluginID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create plugin version: %s", err))
		return
	}

	data.SourceHash = types.StringValue(hash)
	resp.Diagnostics.Append(mapPluginVersionToState(version, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PluginVersionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PluginVersionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	version, err := getPluginVersion(ctx, r.client, data.PluginID.ValueString(), data.ID.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin version: %s", err))
		return
	}

	resp.Diagnostics.Append(mapPluginVersionToState(version, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is never called with a change to apply: every input forces a new
// version.
func (r *PluginVersionResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

// Delete only forgets the version: the API has no per-version delete. The
// version stays in the plugin's history (and is still served if it was the
// served version) until the plugin itself is deleted.
func (r *PluginVersionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PluginVersionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg := fmt.Sprintf("Plugin versions cannot be deleted one by one: version %s was removed from Terraform state "+
		"only, and stays in plugin %s's version history until the plugin is deleted.",
		data.ID.ValueString(), data.PluginID.ValueString())
	resp.Diagnostics.AddWarning("Plugin Version Not Deleted", msg)
	tflog.Warn(ctx, msg)
}

// ============================================================================
// Helper functions
// ============================================================================

func mapPluginVersionToState(v *pluginVersionAPIObject, data *PluginVersionResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	// plugin_id stays as configured: the request path names it, and an echo
	// that came back empty would otherwise force a new version.
	data.ID = types.StringValue(v.ID)
	data.DisplayName = tfvalue.StringOrNull(v.DisplayName)
	data.Description = tfvalue.StringOrNull(v.Description)
	data.ManifestVersion = tfvalue.StringOrNull(v.ManifestVersion)
	data.Reach = tfvalue.StringOrNull(v.Reach)
	data.CreatedAt = types.StringValue(v.CreatedAt)
	// release_notes stays as configured: the version is immutable, and the API
	// returns the notes as null while a customer-managed key is unavailable,
	// which must not read as drift.

	var d diag.Diagnostics
	data.ContentScan, d = contentScanValue(v.ContentScan)
	diags.Append(d...)
	data.Components, d = componentsValue(v.Components)
	diags.Append(d...)
	data.CreatedBy, d = actorValue(v.CreatedBy)
	diags.Append(d...)

	return diags
}

// pluginSourceHashModifier recomputes source_hash from the local files at
// every plan, so an edit to them shows as a change (and, with RequiresReplace
// after it, as a new version) even though source_dir or source_archive did
// not change. A source that cannot be read at plan time leaves the value
// alone when it does not exist, or when the version is new: the same apply may
// produce or complete it, and Create reads it again and fails there if it is
// still missing or invalid. For an existing version, a source that exists but
// cannot be uploaded (a symbolic link, no manifest, an unreadable file) fails
// the plan: keeping the prior hash would plan no change, and the edit would
// never be published.
type pluginSourceHashModifier struct{}

func (m pluginSourceHashModifier) Description(_ context.Context) string {
	return "Recomputes the hash of the plugin source at every plan, so a content change forces a new version."
}

func (m pluginSourceHashModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m pluginSourceHashModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var dir, archive types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("source_dir"), &dir)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("source_archive"), &archive)...)
	if resp.Diagnostics.HasError() || dir.IsUnknown() || archive.IsUnknown() || (dir.IsNull() && archive.IsNull()) {
		return
	}

	src, err := listPluginSource(dir.ValueString(), archive.ValueString())
	if err == nil {
		var hash string
		if hash, err = src.hash(); err == nil {
			resp.PlanValue = types.StringValue(hash)
			return
		}
	}
	if errors.Is(err, fs.ErrNotExist) || req.StateValue.IsNull() {
		return
	}
	source := path.Root("source_dir")
	if !archive.IsNull() {
		source = path.Root("source_archive")
	}
	resp.Diagnostics.AddAttributeError(source, "Invalid plugin source", err.Error())
}
