// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys

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

var _ datasource.DataSource = &ExternalKeyDataSource{}

func NewExternalKeyDataSource() datasource.DataSource {
	return &ExternalKeyDataSource{}
}

type ExternalKeyDataSource struct {
	client *admin.Client
}

func (d *ExternalKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_external_key"
}

func (d *ExternalKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := externalKeyDataSourceAttributes()
	attrs["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: idDescription,
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a customer-managed encryption key (CMEK) registered with the organization, by ID, via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: attrs,
	}
}

// externalKeyDataSourceAttributes returns the computed attributes of an
// external key, shared by the singular data source and the elements of the
// plural one. The singular data source overrides `id` to make it the input.
func externalKeyDataSourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: idDescription,
		},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Human-friendly name, or null if none was set.",
		},
		"geo": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Data residency geo that selects the regional validator for this key.",
		},
		"provider_config": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Where the key lives in the KMS. `type` tells which of the other attributes are set.",
			Attributes: map[string]schema.Attribute{
				"type": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: "KMS provider: `aws`, `gcp` or `azure`.",
				},
				"kms_arn": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: kmsARNDescription,
				},
				"region": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: regionDescription,
				},
				"key_name": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: keyNameDescription,
				},
				"vault_uri": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: vaultURIDescription,
				},
				"tenant_id": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: tenantIDDescription,
				},
				"client_id": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: clientIDDescription,
				},
			},
		},
		"attachment": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: attachmentDescription,
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "RFC 3339 timestamp of when the key was registered.",
		},
		"updated_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "RFC 3339 timestamp of the last update.",
		},
	}
}

func (d *ExternalKeyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ExternalKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ExternalKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := d.client.DoRequest(ctx, "GET", externalKeyPath(config.ID.ValueString()), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError("External Key Not Found",
				fmt.Sprintf("No external key with ID %q exists in the organization.", config.ID.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read external key: %s", err))
		return
	}

	var key externalKeyAPIResponse
	if err := json.Unmarshal(respBytes, &key); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse external key response: %s", err))
		return
	}

	data, diags := mapExternalKeyToModel(ctx, &key)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
