// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &VaultDataSource{}

func NewVaultDataSource() datasource.DataSource {
	return &VaultDataSource{}
}

// VaultDataSource defines the data source implementation.
type VaultDataSource struct {
	client *anthropic.Client
}

func (d *VaultDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vault"
}

func (d *VaultDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := vaultAttributes()
	attrs["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The ID of the vault to retrieve (e.g. `vlt_...`).",
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a vault by ID (beta). Works regardless of whether the vault is archived.",
		Attributes:          attrs,
	}
}

// vaultAttributes returns the computed attributes of a vault, shared by the
// anthropic_vault data source (which makes id its required input) and each
// element of the anthropic_vaults list. They mirror vaultModel.
func vaultAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Unique vault identifier (`vlt_...`).",
		},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Human-readable name of the vault.",
		},
		"metadata": schema.MapAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Arbitrary key-value metadata attached to the vault. Null when the vault has none.",
		},
		"type": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Object type. Always `vault`.",
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Creation timestamp (RFC 3339).",
		},
		"updated_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Last update timestamp (RFC 3339).",
		},
		"archived_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Archive timestamp (RFC 3339). Null if the vault has not been archived.",
		},
	}
}

func (d *VaultDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

func (d *VaultDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data vaultModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := data.ID.ValueString()
	vault, err := d.client.Beta.Vaults.Get(ctx, id, anthropic.BetaVaultGetParams{})
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddError("Vault Not Found", fmt.Sprintf("No vault with ID %q exists in the workspace of the configured API key (or it has been deleted).", id))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vault: %s", err))
		return
	}

	data, diags := mapVaultCommon(vault)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// isNotFound reports whether err is an API 404, which the vault data source
// surfaces as a dedicated diagnostic instead of the generic client error.
func isNotFound(err error) bool {
	var apierr *anthropic.Error
	return errors.As(err, &apierr) && apierr.StatusCode == http.StatusNotFound
}
