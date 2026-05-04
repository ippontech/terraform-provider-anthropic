// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SkillDataSource{}

func NewSkillDataSource() datasource.DataSource {
	return &SkillDataSource{}
}

// SkillDataSource defines the data source implementation.
type SkillDataSource struct {
	client *anthropic.Client
}

// SkillDataSourceModel describes the data source data model.
type SkillDataSourceModel struct {
	SkillID       types.String `tfsdk:"skill_id"`
	ID            types.String `tfsdk:"id"`
	DisplayTitle  types.String `tfsdk:"display_title"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
	LatestVersion types.String `tfsdk:"latest_version"`
	Source        types.String `tfsdk:"source"`
	Type          types.String `tfsdk:"type"`
}

func (d *SkillDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (d *SkillDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a Skill by ID.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"skill_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the skill to retrieve.",
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique skill identifier assigned by the API.",
			},
			"display_title": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable display title for the skill.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill was last updated.",
			},
			"latest_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The latest version identifier for the skill.",
			},
			"source": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Source of the skill: `\"custom\"` (user-created) or `\"anthropic\"` (Anthropic-created).",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. For Skills, this is always `\"skill\"`.",
			},
		},
	}
}

func (d *SkillDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

func (d *SkillDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SkillDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skill, err := d.client.Beta.Skills.Get(ctx, data.SkillID.ValueString(), anthropic.BetaSkillGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve skill: %s", err))
		return
	}

	data.ID = types.StringValue(skill.ID)
	data.CreatedAt = types.StringValue(skill.CreatedAt)
	data.UpdatedAt = types.StringValue(skill.UpdatedAt)
	data.LatestVersion = types.StringValue(skill.LatestVersion)
	data.Source = types.StringValue(skill.Source)
	data.Type = types.StringValue(skill.Type)

	if skill.DisplayTitle != "" {
		data.DisplayTitle = types.StringValue(skill.DisplayTitle)
	} else {
		data.DisplayTitle = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
