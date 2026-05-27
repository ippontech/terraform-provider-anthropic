// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &APIKeyDataSource{}

func NewAPIKeyDataSource() datasource.DataSource {
	return &APIKeyDataSource{}
}

type APIKeyDataSource struct {
	adminClient *admin.Client
}

func (d *APIKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (d *APIKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single Anthropic API key by ID via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The API key identifier.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name of the API key.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Status of the API key (`active` or `inactive`).",
			},
			"workspace_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The ID of the workspace this key is scoped to, or null for organization-level keys.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
			},
			"created_by": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Details about the actor who created this API key.",
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Identifier of the creator.",
					},
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Type of the creator (e.g. `user`).",
					},
				},
			},
			"partial_key_hint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The last four visible characters of the API key, for identification.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `api_key`.",
			},
		},
	}
}

func (d *APIKeyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.adminClient = pd.AdminClient
}

func (d *APIKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data APIKeyResourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := d.adminClient.DoRequest(ctx, "GET", "/v1/organizations/api_keys/"+data.ID.ValueString(), nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read API key: %s", err))
		return
	}

	var key apiKeyAPIResponse
	if err := json.Unmarshal(respBytes, &key); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse API key response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAPIKeyToState(ctx, &key, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
