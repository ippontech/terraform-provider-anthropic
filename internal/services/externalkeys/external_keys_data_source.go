// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &ExternalKeysDataSource{}

func NewExternalKeysDataSource() datasource.DataSource {
	return &ExternalKeysDataSource{}
}

type ExternalKeysDataSource struct {
	client *admin.Client
}

// ExternalKeysDataSourceModel describes the data source data model.
type ExternalKeysDataSourceModel struct {
	ExternalKeys types.List `tfsdk:"external_keys"`
}

// externalKeysPageLimit is the largest page the list endpoint serves.
const externalKeysPageLimit = "100"

func (d *ExternalKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_external_keys"
}

func (d *ExternalKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the customer-managed encryption keys (CMEK) registered with the organization via the Admin API, newest first. " +
			"All pages are fetched automatically. Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"external_keys": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "External keys of the organization, newest first. Empty when none is registered.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: externalKeyDataSourceAttributes(),
				},
			},
		},
	}
}

func (d *ExternalKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics) {
		return
	}

	d.client = pd.AdminClient
}

// buildExternalKeysQuery builds the query of one list page: the largest page
// size, plus the opaque `page` cursor the previous response returned as
// `next_page` (omitted on the first request). This endpoint pages with
// `page`/`next_page`, not the `after_id`/`has_more` pair of the older
// organization lists.
func buildExternalKeysQuery(page string) url.Values {
	params := url.Values{}
	params.Set("limit", externalKeysPageLimit)
	if page != "" {
		params.Set("page", page)
	}
	return params
}

func (d *ExternalKeysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	elems := []attr.Value{}
	page := ""
	for {
		respBytes, err := d.client.DoRequest(ctx, "GET", externalKeysPath+"?"+buildExternalKeysQuery(page).Encode(), nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list external keys: %s", err))
			return
		}

		var list externalKeysListResponse
		if err := json.Unmarshal(respBytes, &list); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse external keys response: %s", err))
			return
		}

		for i := range list.Data {
			model, diags := mapExternalKeyToModel(ctx, &list.Data[i])
			resp.Diagnostics.Append(diags...)
			obj, diags := types.ObjectValueFrom(ctx, externalKeyAttrTypes, model)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			elems = append(elems, obj)
		}

		// Stop on an absent cursor, and on a repeated one rather than loop forever.
		if list.NextPage == nil || *list.NextPage == "" || *list.NextPage == page {
			break
		}
		page = *list.NextPage
	}

	keys, diags := types.ListValue(types.ObjectType{AttrTypes: externalKeyAttrTypes}, elems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ExternalKeysDataSourceModel{ExternalKeys: keys})...)
}
