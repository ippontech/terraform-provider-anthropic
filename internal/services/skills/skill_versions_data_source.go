// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

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
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SkillVersionsDataSource{}

func NewSkillVersionsDataSource() datasource.DataSource {
	return &SkillVersionsDataSource{}
}

// SkillVersionsDataSource defines the data source implementation.
type SkillVersionsDataSource struct {
	client *anthropic.Client
}

// SkillVersionsDataSourceModel describes the data source data model.
type SkillVersionsDataSourceModel struct {
	SkillID  types.String `tfsdk:"skill_id"`
	Versions types.List   `tfsdk:"versions"`
}

// skillVersionListItemAttrTypes describes the attribute types of each element in the
// "versions" list.
var skillVersionListItemAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"version":     types.StringType,
	"name":        types.StringType,
	"description": types.StringType,
	"directory":   types.StringType,
	"created_at":  types.StringType,
	"type":        types.StringType,
}

func (d *SkillVersionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill_versions"
}

func (d *SkillVersionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all Skill Versions for a given Skill on the Anthropic platform (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			// --- Required input ---
			"skill_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the Skill to list versions for.",
			},

			// --- Computed output ---
			"versions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of Skill Versions for the given Skill.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique skill version identifier assigned by the API.",
						},
						"version": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Version identifier for the skill, expressed as a Unix epoch timestamp (e.g., `\"1759178010641129\"`).",
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
				},
			},
		},
	}
}

func (d *SkillVersionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SkillVersionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SkillVersionsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pager := d.client.Beta.Skills.Versions.ListAutoPaging(ctx, data.SkillID.ValueString(), anthropic.BetaSkillVersionListParams{})

	versionObjs := make([]attr.Value, 0)
	for pager.Next() {
		item := pager.Current()
		obj, diags := types.ObjectValue(skillVersionListItemAttrTypes, map[string]attr.Value{
			"id":          types.StringValue(item.ID),
			"version":     types.StringValue(item.Version),
			"name":        types.StringValue(item.Name),
			"description": types.StringValue(item.Description),
			"directory":   types.StringValue(item.Directory),
			"created_at":  types.StringValue(item.CreatedAt),
			"type":        types.StringValue(item.Type),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		versionObjs = append(versionObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list skill versions: %s", err))
		return
	}

	versionsList, diags := types.ListValue(types.ObjectType{AttrTypes: skillVersionListItemAttrTypes}, versionObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Versions = versionsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
