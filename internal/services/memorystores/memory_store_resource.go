// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &MemoryStoreResource{}
var _ resource.ResourceWithImportState = &MemoryStoreResource{}

func NewMemoryStoreResource() resource.Resource {
	return &MemoryStoreResource{}
}

// MemoryStoreResource defines the resource implementation.
type MemoryStoreResource struct {
	client *anthropic.Client
}

// MemoryStoreResourceModel describes the resource data model.
type MemoryStoreResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Metadata         types.Map    `tfsdk:"metadata"`
	CreatedAt        types.String `tfsdk:"created_at"`
	ArchivedAt       types.String `tfsdk:"archived_at"`
	ArchiveOnDestroy types.Bool   `tfsdk:"archive_on_destroy"`
}

// --- Schema ---

func (r *MemoryStoreResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memory_store"
}

func (r *MemoryStoreResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a memory store: a named container for agent memories, scoped to a workspace, " +
			"for use by Anthropic Managed Agent sessions (beta). Attach a store to a session via `resources[]` to mount it as a " +
			"directory the agent can read and write.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Human-readable name for the store. 1–255 characters. The store's mount-path slug under " +
					"`/mnt/memory/` is derived from this name.",
			},
			"description": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Free-text description of what the store contains, up to 1024 characters. Included in the " +
					"agent's system prompt when the store is attached, so word it to be useful to the agent.",
			},

			// --- Optional ---
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value tags for your own bookkeeping. Up to 16 pairs; keys 1–64 characters; values up to 512 characters. Not visible to the agent.",
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for the memory store assigned by the API (e.g. `memstore_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the store has not been archived.",
			},
			"archive_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "If `true`, destroying this resource archives the memory store instead of permanently deleting it. Default: `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *MemoryStoreResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

// --- Create ---

func (r *MemoryStoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MemoryStoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaMemoryStoreNewParams{
		Name:        data.Name.ValueString(),
		Description: anthropic.String(data.Description.ValueString()),
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	}

	store, err := r.client.Beta.MemoryStores.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create memory store: %s", err))
		return
	}

	resp.Diagnostics.Append(mapMemoryStoreToState(store, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *MemoryStoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MemoryStoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	store, err := r.client.Beta.MemoryStores.Get(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreGetParams{})
	if err != nil {
		// The store was deleted out-of-band: drop it from state so the next plan
		// recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read memory store: %s", err))
		return
	}

	resp.Diagnostics.Append(mapMemoryStoreToState(store, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// archive_on_destroy is local-only (not in the API); default to false when not already set (e.g. on import).
	if data.ArchiveOnDestroy.IsNull() || data.ArchiveOnDestroy.IsUnknown() {
		data.ArchiveOnDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *MemoryStoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data MemoryStoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MemoryStoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The API rejects updates to an archived store. Fail fast with a clear
	// message instead of letting a confusing 4xx surface from the SDK, but
	// only when a mutable field actually changed — re-applying an unchanged
	// config against an archived store is a no-op, not an error.
	if !state.ArchivedAt.IsNull() && state.ArchivedAt.ValueString() != "" {
		if !data.Name.Equal(state.Name) || !data.Description.Equal(state.Description) || !data.Metadata.Equal(state.Metadata) {
			resp.Diagnostics.AddError(
				"Memory Store Is Archived",
				fmt.Sprintf("Memory store %q was archived on %s and can no longer be updated. "+
					"Remove it from configuration, or restore the prior name/description/metadata to stop this diff.",
					state.ID.ValueString(), state.ArchivedAt.ValueString()),
			)
			return
		}
	}

	params := anthropic.BetaMemoryStoreUpdateParams{
		Name:        anthropic.String(data.Name.ValueString()),
		Description: anthropic.String(data.Description.ValueString()),
	}

	// metadata uses PATCH semantics (omitted keys preserved, null deletes a
	// key). BetaMemoryStoreUpdateParams.Metadata is a plain map[string]string,
	// which cannot represent a per-key null in Go, so a patch that needs to
	// clear a key is sent via SetExtraFields as map[string]any instead — same
	// escape hatch as vaults' buildMetadataPatch. This depends on the SDK
	// keeping Metadata typed as map[string]string: if a future SDK version
	// retypes it to map[string]any (able to carry a JSON null natively), this
	// escape hatch becomes unnecessary and should be removed in favor of the
	// typed field.
	metaPatch, d := buildMemoryStoreMetadataPatch(ctx, data.Metadata, state.Metadata)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(metaPatch) > 0 {
		params.SetExtraFields(map[string]any{"metadata": metaPatch})
	}

	store, err := r.client.Beta.MemoryStores.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update memory store: %s", err))
		return
	}

	resp.Diagnostics.Append(mapMemoryStoreToState(store, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete ---

func (r *MemoryStoreResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data MemoryStoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ArchiveOnDestroy.ValueBool() {
		if data.ArchivedAt.IsNull() || data.ArchivedAt.ValueString() == "" {
			_, err := r.client.Beta.MemoryStores.Archive(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreArchiveParams{})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive memory store: %s", err))
			}
		}
		return
	}

	_, err := r.client.Beta.MemoryStores.Delete(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreDeleteParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete memory store: %s", err))
	}
}

// --- ImportState ---

func (r *MemoryStoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// memoryStoreCommonModel holds the fields common to the resource and both data
// sources (everything except archive_on_destroy, which is local-only and has
// no counterpart in the API response).
type memoryStoreCommonModel struct {
	ID          types.String
	Name        types.String
	Description types.String
	Metadata    types.Map
	CreatedAt   types.String
	ArchivedAt  types.String
}

// mapMemoryStoreCommon maps the API response to the fields shared by the
// resource and both data sources. Extracted so the mapping logic (in
// particular the archived_at zero-value and metadata empty-map handling) is
// exercised once by unit tests and reused everywhere a memory store is read.
func mapMemoryStoreCommon(store *anthropic.BetaManagedAgentsMemoryStore) (memoryStoreCommonModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	m := memoryStoreCommonModel{
		ID:          types.StringValue(store.ID),
		Name:        types.StringValue(store.Name),
		Description: types.StringValue(store.Description),
		CreatedAt:   types.StringValue(store.CreatedAt.Format(time.RFC3339)),
	}

	if store.ArchivedAt.IsZero() {
		m.ArchivedAt = types.StringNull()
	} else {
		m.ArchivedAt = types.StringValue(store.ArchivedAt.Format(time.RFC3339))
	}

	if len(store.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(store.Metadata))
		for k, v := range store.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		m.Metadata = metaMap
	} else {
		m.Metadata = types.MapNull(types.StringType)
	}

	return m, diags
}

// mapMemoryStoreToState maps the API response into the resource's state
// model, leaving ArchiveOnDestroy (local-only) untouched.
func mapMemoryStoreToState(store *anthropic.BetaManagedAgentsMemoryStore, data *MemoryStoreResourceModel) diag.Diagnostics {
	common, diags := mapMemoryStoreCommon(store)
	data.ID = common.ID
	data.Name = common.Name
	data.Description = common.Description
	data.Metadata = common.Metadata
	data.CreatedAt = common.CreatedAt
	data.ArchivedAt = common.ArchivedAt
	return diags
}

// buildMemoryStoreMetadataPatch computes a PATCH body for the metadata field.
// The API preserves omitted keys and deletes keys whose value is null, so to
// converge declarative config with server state the patch upserts every
// planned key and explicitly sets keys removed since the prior state to null.
// Returns a map[string]any (values are string for upserts, nil for deletes)
// suitable for SetExtraFields; an empty map means "no change".
func buildMemoryStoreMetadataPatch(ctx context.Context, plan, state types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	patch := map[string]any{}

	if !plan.IsNull() && !plan.IsUnknown() {
		var pm map[string]string
		diags.Append(plan.ElementsAs(ctx, &pm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k, v := range pm {
			patch[k] = v
		}
	}

	if !state.IsNull() && !state.IsUnknown() {
		var sm map[string]string
		diags.Append(state.ElementsAs(ctx, &sm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k := range sm {
			if _, stillPresent := patch[k]; !stillPresent {
				patch[k] = nil
			}
		}
	}

	return patch, diags
}
