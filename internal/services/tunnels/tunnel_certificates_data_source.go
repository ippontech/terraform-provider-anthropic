// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"errors"
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
	"github.com/ippontech/terraform-provider-anthropic/internal/sdkpage"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// tunnelCertificatesPageSize is the page size requested from the certificate
// list endpoint (default 20, maximum 1000).
const tunnelCertificatesPageSize = 100

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &TunnelCertificatesDataSource{}

func NewTunnelCertificatesDataSource() datasource.DataSource {
	return &TunnelCertificatesDataSource{}
}

// TunnelCertificatesDataSource defines the data source implementation.
type TunnelCertificatesDataSource struct {
	client *providerdata.OAuthClient
}

// errCertificateMapping stops the list walk once a certificate fails to map;
// its diagnostics are already in the response.
var errCertificateMapping = errors.New("tunnel certificate mapping failed")

// tunnelCertificatesDSModel describes the "anthropic_tunnel_certificates"
// data source data model. It has no PEM: the API never returns it.
type tunnelCertificatesDSModel struct {
	TunnelID        types.String `tfsdk:"tunnel_id"`
	IncludeArchived types.Bool   `tfsdk:"include_archived"`
	Certificates    types.List   `tfsdk:"certificates"`
}

// tunnelCertificateDSAttrTypes describes one object of the "certificates"
// list.
var tunnelCertificateDSAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"tunnel_id":   types.StringType,
	"fingerprint": types.StringType,
	"created_at":  types.StringType,
	"expires_at":  types.StringType,
	"archived_at": types.StringType,
}

func (d *TunnelCertificatesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tunnel_certificates"
}

func (d *TunnelCertificatesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the CA certificates registered on an MCP tunnel (research preview). All pages are fetched " +
			"automatically. The API never returns a certificate's PEM, only its fingerprint. Requires an OAuth bearer token " +
			"with the `workspace:manage_tunnels` scope.",
		Attributes: map[string]schema.Attribute{
			"tunnel_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the tunnel whose certificates to list (`tnl_...`).",
			},
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to include archived certificates. Defaults to `false`, the API's own default.",
			},
			"certificates": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of certificates, in the order the API returns them.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Unique certificate identifier (`tcrt_...`).",
					},
					"tunnel_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the tunnel the certificate is registered on.",
					},
					"fingerprint": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Lowercase hex SHA-256 fingerprint of the certificate's DER encoding.",
					},
					"created_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Registration timestamp (RFC 3339).",
					},
					"expires_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Expiry of the certificate (RFC 3339), or null if it does not expire.",
					},
					"archived_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Archive timestamp (RFC 3339), or null while the tunnel trusts the certificate.",
					},
				}},
			},
		},
	}
}

func (d *TunnelCertificatesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TunnelCertificatesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data tunnelCertificatesDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// include_archived is sent only when true: false is the API's default.
	params := anthropic.BetaTunnelCertificateListParams{Limit: param.NewOpt(int64(tunnelCertificatesPageSize))}
	if data.IncludeArchived.ValueBool() {
		params.IncludeArchived = param.NewOpt(true)
	}

	// sdkpage.Cursor rather than ListAutoPaging: a next_page cursor the API
	// repeats is an error instead of a loop that hangs the plan.
	certificateObjs := make([]attr.Value, 0)
	first, err := d.client.Beta.Tunnels.Certificates.List(ctx, data.TunnelID.ValueString(), params)
	err = sdkpage.Cursor(first, err, func(cert anthropic.BetaTunnelCertificate) error {
		obj, diags := mapTunnelCertificateDSToListObject(&cert)
		resp.Diagnostics.Append(diags...)
		if diags.HasError() {
			return errCertificateMapping
		}
		certificateObjs = append(certificateObjs, obj)
		return nil
	})
	if errors.Is(err, errCertificateMapping) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list tunnel certificates: %s", err))
		return
	}

	certificatesList, diags := types.ListValue(types.ObjectType{AttrTypes: tunnelCertificateDSAttrTypes}, certificateObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Certificates = certificatesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapTunnelCertificateDSToListObject maps one certificate into the
// "certificates" list's nested object value.
func mapTunnelCertificateDSToListObject(cert *anthropic.BetaTunnelCertificate) (attr.Value, diag.Diagnostics) {
	return types.ObjectValue(tunnelCertificateDSAttrTypes, map[string]attr.Value{
		"id":          types.StringValue(cert.ID),
		"tunnel_id":   types.StringValue(cert.TunnelID),
		"fingerprint": types.StringValue(normalizeFingerprint(cert.Fingerprint)),
		"created_at":  tfvalue.TimeOrNull(cert.CreatedAt),
		"expires_at":  tfvalue.TimeOrNull(cert.ExpiresAt),
		"archived_at": tfvalue.TimeOrNull(cert.ArchivedAt),
	})
}
