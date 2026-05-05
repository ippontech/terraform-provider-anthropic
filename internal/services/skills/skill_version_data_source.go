// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SkillVersionDataSource{}

func NewSkillVersionDataSource() datasource.DataSource {
	return &SkillVersionDataSource{}
}

// SkillVersionDataSource defines the data source implementation.
type SkillVersionDataSource struct {
	client *anthropic.Client
}

// SkillVersionDataSourceModel describes the data source data model.
type SkillVersionDataSourceModel struct {
	SkillID     types.String `tfsdk:"skill_id"`
	Version     types.String `tfsdk:"version"`
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Directory   types.String `tfsdk:"directory"`
	CreatedAt   types.String `tfsdk:"created_at"`
	Type        types.String `tfsdk:"type"`
}

func (d *SkillVersionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill_version"
}

func (d *SkillVersionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a specific Skill Version by skill ID and version.",
		Attributes: map[string]schema.Attribute{
			"skill_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the Skill that this version belongs to.",
			},
			"version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Version identifier for the skill, expressed as a Unix epoch timestamp (e.g., `\"1759178010641129\"`).",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique skill version identifier assigned by the API.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name of the skill version, extracted from the `SKILL.md` file.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Description of the skill version, extracted from the `SKILL.md` file.",
			},
			"directory": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Top-level directory name extracted from the uploaded files.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill version was created.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. For Skill Versions, this is always `\"skill_version\"`.",
			},
		},
	}
}

func (d *SkillVersionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SkillVersionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SkillVersionDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skillVersion, err := d.client.Beta.Skills.Versions.Get(ctx, data.Version.ValueString(), anthropic.BetaSkillVersionGetParams{
		SkillID: data.SkillID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve skill version: %s", err))
		return
	}

	data.ID = types.StringValue(skillVersion.ID)
	data.Name = types.StringValue(skillVersion.Name)
	data.Description = types.StringValue(skillVersion.Description)
	data.Directory = types.StringValue(skillVersion.Directory)
	data.CreatedAt = types.StringValue(skillVersion.CreatedAt)
	data.Type = types.StringValue(skillVersion.Type)
	data.SkillID = types.StringValue(skillVersion.SkillID)
	data.Version = types.StringValue(skillVersion.Version)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
