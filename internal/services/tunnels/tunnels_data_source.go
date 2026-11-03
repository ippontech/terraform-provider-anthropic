// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// tunnelsPageSize is the page size requested from the list endpoint
// (default 20, maximum 1000).
const tunnelsPageSize = 100

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &TunnelsDataSource{}

func NewTunnelsDataSource() datasource.DataSource {
	return &TunnelsDataSource{}
}

// TunnelsDataSource defines the data source implementation.
type TunnelsDataSource struct {
	client *providerdata.OAuthClient
}

// tunnelsDSModel describes the "anthropic_tunnels" data source data model.
type tunnelsDSModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	Tunnels         types.List `tfsdk:"tunnels"`
}

func (d *TunnelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tunnels"
}

func (d *TunnelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	itemAttributes := tunnelDSAttributes()
	itemAttributes["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "Unique tunnel identifier (`tnl_...`).",
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the MCP tunnels of the workspace the credential acts on (research preview), newest first. All pages " +
			"are fetched automatically. Tunnel tokens are not exposed. Requires an OAuth bearer token with the " +
			"`workspace:manage_tunnels` scope.",
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to include archived tunnels. Defaults to `false`, the API's own default.",
			},
			"tunnels": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of tunnels, newest first.",
				NestedObject:        schema.NestedAttributeObject{Attributes: itemAttributes},
			},
		},
	}
}

func (d *TunnelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TunnelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data tunnelsDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaTunnelListParams{Limit: param.NewOpt(int64(tunnelsPageSize))}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		params.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}

	pager := d.client.Beta.Tunnels.ListAutoPaging(ctx, params)
	tunnelObjs := make([]attr.Value, 0)
	for pager.Next() {
		tunnel := pager.Current()
		obj, diags := mapTunnelDSToListObject(&tunnel)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		tunnelObjs = append(tunnelObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list tunnels: %s", err))
		return
	}

	tunnelsList, diags := types.ListValue(types.ObjectType{AttrTypes: tunnelDSAttrTypes}, tunnelObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Tunnels = tunnelsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapTunnelDSToListObject maps one tunnel into the "tunnels" list's nested
// object value.
func mapTunnelDSToListObject(tunnel *anthropic.BetaTunnel) (attr.Value, diag.Diagnostics) {
	return types.ObjectValue(tunnelDSAttrTypes, map[string]attr.Value{
		"id":           types.StringValue(tunnel.ID),
		"display_name": tfvalue.StringOrNull(tunnel.DisplayName),
		"domain":       types.StringValue(tunnel.Domain),
		"created_at":   tfvalue.TimeOrNull(tunnel.CreatedAt),
		"archived_at":  tfvalue.TimeOrNull(tunnel.ArchivedAt),
	})
}
