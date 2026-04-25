// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SkillsDataSource{}

func NewSkillsDataSource() datasource.DataSource {
	return &SkillsDataSource{}
}

// SkillsDataSource defines the data source implementation.
type SkillsDataSource struct {
	client *anthropic.Client
}

// SkillsDataSourceModel describes the data source data model.
type SkillsDataSourceModel struct {
	SourceFilter types.String `tfsdk:"source_filter"`
	Skills       types.List   `tfsdk:"skills"`
}

// skillListItemAttrTypes describes the attribute types of each element in the
// "skills" list.
var skillListItemAttrTypes = map[string]attr.Type{
	"id":             types.StringType,
	"display_title":  types.StringType,
	"created_at":     types.StringType,
	"updated_at":     types.StringType,
	"latest_version": types.StringType,
	"source":         types.StringType,
	"type":           types.StringType,
}

func (d *SkillsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skills"
}

func (d *SkillsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists available Skills on the Anthropic platform (beta). Supports optional filtering by source (`\"custom\"` or `\"anthropic\"`); all pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			// --- Optional inputs ---
			"source_filter": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringvalidator.OneOf("custom", "anthropic"),
				},
				MarkdownDescription: "Filter skills by source. Accepted values: `\"custom\"` (user-created) or `\"anthropic\"` (built-in). Values are case-sensitive.",
			},

			// --- Computed output ---
			"skills": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of Skills matching the filter.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
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
							MarkdownDescription: "Source of the skill: `\"custom\"` for user-created skills, `\"anthropic\"` for built-in skills.",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type returned by the API.",
						},
					},
				},
			},
		},
	}
}

func (d *SkillsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.client = pd.Client
}

func (d *SkillsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SkillsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaSkillListParams{}
	if !data.SourceFilter.IsNull() && !data.SourceFilter.IsUnknown() {
		params.Source = anthropic.String(data.SourceFilter.ValueString())
	}

	pager := d.client.Beta.Skills.ListAutoPaging(ctx, params)
	skillObjs := make([]attr.Value, 0)
	for pager.Next() {
		s := pager.Current()
		displayTitle := types.StringNull()
		if s.DisplayTitle != "" {
			displayTitle = types.StringValue(s.DisplayTitle)
		}
		obj, diags := types.ObjectValue(skillListItemAttrTypes, map[string]attr.Value{
			"id":             types.StringValue(s.ID),
			"display_title":  displayTitle,
			"created_at":     types.StringValue(s.CreatedAt),
			"updated_at":     types.StringValue(s.UpdatedAt),
			"latest_version": types.StringValue(s.LatestVersion),
			"source":         types.StringValue(s.Source),
			"type":           types.StringValue(s.Type),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		skillObjs = append(skillObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list skills: %s", err))
		return
	}

	skillsList, diags := types.ListValue(types.ObjectType{AttrTypes: skillListItemAttrTypes}, skillObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Skills = skillsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
