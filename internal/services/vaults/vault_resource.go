// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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
var _ resource.Resource = &VaultResource{}
var _ resource.ResourceWithImportState = &VaultResource{}

func NewVaultResource() resource.Resource {
	return &VaultResource{}
}

// VaultResource defines the resource implementation.
type VaultResource struct {
	client *anthropic.Client
}

// VaultResourceModel describes the resource data model.
type VaultResourceModel struct {
	ID               types.String `tfsdk:"id"`
	DisplayName      types.String `tfsdk:"display_name"`
	Metadata         types.Map    `tfsdk:"metadata"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	ArchivedAt       types.String `tfsdk:"archived_at"`
	Type             types.String `tfsdk:"type"`
	ArchiveOnDestroy types.Bool   `tfsdk:"archive_on_destroy"`
}

// --- Schema ---

func (r *VaultResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vault"
}

func (r *VaultResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a vault that securely stores MCP credentials for use by Anthropic Managed Agent sessions (beta). " +
			"Vaults are billed only at runtime when credentials are accessed during an agent session.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"display_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name for the vault. 1–255 characters.",
			},

			// --- Optional ---
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata to attach to the vault. Maximum 16 pairs, keys up to 64 characters, values up to 512 characters.",
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique vault identifier assigned by the API (e.g. `vlt_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `vault`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the vault has not been archived.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"archive_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "If `true`, destroying this resource archives the vault instead of permanently deleting it. Default: `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *VaultResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VaultResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VaultResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaVaultNewParams{
		DisplayName: data.DisplayName.ValueString(),
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	}

	vault, err := r.client.Beta.Vaults.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create vault: %s", err))
		return
	}

	resp.Diagnostics.Append(mapVaultResponseToState(vault, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *VaultResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VaultResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vault, err := r.client.Beta.Vaults.Get(ctx, data.ID.ValueString(), anthropic.BetaVaultGetParams{})
	if err != nil {
		// The vault was deleted out-of-band: drop it from state so the next plan
		// recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vault: %s", err))
		return
	}

	resp.Diagnostics.Append(mapVaultResponseToState(vault, &data)...)
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

func (r *VaultResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data VaultResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state VaultResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaVaultUpdateParams{
		DisplayName: param.NewOpt(data.DisplayName.ValueString()),
	}

	// metadata uses PATCH semantics (omitted keys preserved, null deletes a key).
	// Build a patch that upserts planned keys and explicitly nulls keys removed
	// since the prior state, so a cleared/removed key actually converges.
	metaPatch, d := buildMetadataPatch(ctx, data.Metadata, state.Metadata)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(metaPatch) > 0 {
		params.SetExtraFields(map[string]any{"metadata": metaPatch})
	}

	vault, err := r.client.Beta.Vaults.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update vault: %s", err))
		return
	}

	resp.Diagnostics.Append(mapVaultResponseToState(vault, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete ---

func (r *VaultResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VaultResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ArchiveOnDestroy.ValueBool() {
		_, err := r.client.Beta.Vaults.Archive(ctx, data.ID.ValueString(), anthropic.BetaVaultArchiveParams{})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive vault: %s", err))
		}
		return
	}

	_, err := r.client.Beta.Vaults.Delete(ctx, data.ID.ValueString(), anthropic.BetaVaultDeleteParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete vault: %s", err))
	}
}

// --- ImportState ---

func (r *VaultResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// mapVaultResponseToState maps the API response to the Terraform state model.
func mapVaultResponseToState(vault *anthropic.BetaManagedAgentsVault, data *VaultResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(vault.ID)
	data.DisplayName = types.StringValue(vault.DisplayName)
	data.Type = types.StringValue(string(vault.Type))
	data.CreatedAt = types.StringValue(vault.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(vault.UpdatedAt.Format(time.RFC3339))

	if vault.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(vault.ArchivedAt.Format(time.RFC3339))
	}

	// Metadata
	if len(vault.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(vault.Metadata))
		for k, v := range vault.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Metadata = metaMap
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}

	return diags
}

// buildMetadataPatch computes a PATCH body for the metadata field shared by
// vaults and vault credentials. The API preserves omitted keys and deletes keys
// whose value is null, so to converge declarative config with server state the
// patch upserts every planned key and explicitly sets keys removed since the
// prior state to null. Returns a map[string]any (values are string for upserts,
// nil for deletes) suitable for SetExtraFields; an empty map means "no change".
func buildMetadataPatch(ctx context.Context, plan, state types.Map) (map[string]any, diag.Diagnostics) {
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
			if _, ok := patch[k]; !ok {
				patch[k] = nil
			}
		}
	}

	return patch, diags
}
