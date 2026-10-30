// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &TunnelDataSource{}

func NewTunnelDataSource() datasource.DataSource {
	return &TunnelDataSource{}
}

// TunnelDataSource defines the data source implementation.
type TunnelDataSource struct {
	client *providerdata.OAuthClient
}

// tunnelDSModel describes the "anthropic_tunnel" data source data model. It
// deliberately has no token: revealing it is an audited action that belongs
// to the resource, not to a lookup that runs on every plan.
type tunnelDSModel struct {
	ID          types.String `tfsdk:"id"`
	DisplayName types.String `tfsdk:"display_name"`
	Domain      types.String `tfsdk:"domain"`
	CreatedAt   types.String `tfsdk:"created_at"`
	ArchivedAt  types.String `tfsdk:"archived_at"`
}

// tunnelDSAttrTypes describes one tunnel object, shared with the "tunnels"
// list of anthropic_tunnels.
var tunnelDSAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"display_name": types.StringType,
	"domain":       types.StringType,
	"created_at":   types.StringType,
	"archived_at":  types.StringType,
}

// tunnelDSAttributes returns the computed attributes describing one tunnel,
// shared by both data sources. Each adds its own "id": the singular data
// source takes it as input, the list computes it.
func tunnelDSAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Human-readable name for the tunnel, or null if unset.",
		},
		"domain": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Anthropic-assigned hostname for the tunnel. MCP server URLs whose host is a subdomain of this value are routed through the tunnel.",
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Creation timestamp (RFC 3339).",
		},
		"archived_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Archive timestamp (RFC 3339), or null while the tunnel is live.",
		},
	}
}

func (d *TunnelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tunnel"
}

func (d *TunnelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := tunnelDSAttributes()
	attributes["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The ID of the tunnel to retrieve (`tnl_...`).",
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves an MCP tunnel by ID, archived or not (research preview). The tunnel token is not exposed. " +
			"Requires an OAuth bearer token with the `workspace:manage_tunnels` scope.",
		Attributes: attributes,
	}
}

func (d *TunnelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireOAuthDataSourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	d.client = pd.OAuthClient
}

func (d *TunnelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data tunnelDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tunnel, err := d.client.Beta.Tunnels.Get(ctx, data.ID.ValueString(), anthropic.BetaTunnelGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve tunnel: %s", err))
		return
	}

	mapTunnelDSToState(tunnel, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapTunnelDSToState maps the API response into the singular data source's
// state model.
func mapTunnelDSToState(tunnel *anthropic.BetaTunnel, data *tunnelDSModel) {
	data.ID = types.StringValue(tunnel.ID)
	data.DisplayName = tfvalue.StringOrNull(tunnel.DisplayName)
	data.Domain = types.StringValue(tunnel.Domain)
	data.CreatedAt = tfvalue.TimeOrNull(tunnel.CreatedAt)
	data.ArchivedAt = tfvalue.TimeOrNull(tunnel.ArchivedAt)
}
