// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &EnvironmentDataSource{}

func NewEnvironmentDataSource() datasource.DataSource {
	return &EnvironmentDataSource{}
}

type EnvironmentDataSource struct {
	client *anthropic.Client
}

type EnvironmentDataSourceModel struct {
	EnvironmentID types.String `tfsdk:"environment_id"`
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Metadata      types.Map    `tfsdk:"metadata"`
	Config        types.Object `tfsdk:"config"`
	ArchivedAt    types.String `tfsdk:"archived_at"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
	Type          types.String `tfsdk:"type"`
}

var envDSNetworkingAttrTypes = map[string]attr.Type{
	"type":                   types.StringType,
	"allow_mcp_servers":      types.BoolType,
	"allow_package_managers": types.BoolType,
	"allowed_hosts":          types.ListType{ElemType: types.StringType},
}

var envDSPackagesAttrTypes = map[string]attr.Type{
	"apt":   types.ListType{ElemType: types.StringType},
	"cargo": types.ListType{ElemType: types.StringType},
	"gem":   types.ListType{ElemType: types.StringType},
	"go":    types.ListType{ElemType: types.StringType},
	"npm":   types.ListType{ElemType: types.StringType},
	"pip":   types.ListType{ElemType: types.StringType},
}

var envDSConfigAttrTypes = map[string]attr.Type{
	"type":       types.StringType,
	"networking": types.ObjectType{AttrTypes: envDSNetworkingAttrTypes},
	"packages":   types.ObjectType{AttrTypes: envDSPackagesAttrTypes},
}

func (d *EnvironmentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (d *EnvironmentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single Anthropic cloud environment by ID (beta).",
		Attributes: map[string]schema.Attribute{
			"environment_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the environment to retrieve.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique environment identifier.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name for the environment.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "User-provided description for the environment.",
			},
			"metadata": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata attached to the environment.",
			},
			"config": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Cloud environment configuration.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Environment configuration type (always `cloud`).",
					},
					"networking": schema.SingleNestedAttribute{
						Computed:            true,
						MarkdownDescription: "Network configuration policy.",
						Attributes: map[string]schema.Attribute{
							"type": schema.StringAttribute{
								Computed:            true,
								MarkdownDescription: "Network policy type: `unrestricted` or `limited`.",
							},
							"allow_mcp_servers": schema.BoolAttribute{
								Computed:            true,
								MarkdownDescription: "Whether outbound access to MCP server endpoints is permitted.",
							},
							"allow_package_managers": schema.BoolAttribute{
								Computed:            true,
								MarkdownDescription: "Whether outbound access to public package registries is permitted.",
							},
							"allowed_hosts": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Domains the container can reach.",
							},
						},
					},
					"packages": schema.SingleNestedAttribute{
						Computed:            true,
						MarkdownDescription: "Package manager configuration.",
						Attributes: map[string]schema.Attribute{
							"apt": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Ubuntu/Debian packages to install.",
							},
							"cargo": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Rust packages to install.",
							},
							"gem": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Ruby packages to install.",
							},
							"go": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Go packages to install.",
							},
							"npm": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Node.js packages to install.",
							},
							"pip": schema.ListAttribute{
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Python packages to install.",
							},
						},
					},
				},
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp when the environment was archived. Null if not archived.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp when the environment was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp when the environment was last updated.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type (always `environment`).",
			},
		},
	}
}

func (d *EnvironmentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *EnvironmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data EnvironmentDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := d.client.Beta.Environments.Get(ctx, data.EnvironmentID.ValueString(), anthropic.BetaEnvironmentGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve environment: %s", err))
		return
	}

	data.ID = types.StringValue(env.ID)
	data.Name = types.StringValue(env.Name)
	data.Description = types.StringValue(env.Description)
	data.Type = types.StringValue(string(env.Type))
	data.CreatedAt = types.StringValue(env.CreatedAt)
	data.UpdatedAt = types.StringValue(env.UpdatedAt)

	if env.ArchivedAt == "" {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(env.ArchivedAt)
	}

	if len(env.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(env.Metadata))
		for k, v := range env.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, diags := types.MapValue(types.StringType, elements)
		resp.Diagnostics.Append(diags...)
		data.Metadata = metaMap
	} else {
		data.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{})
	}

	if resp.Diagnostics.HasError() {
		return
	}

	allowedHosts, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Networking.AllowedHosts)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkingObj, diags := types.ObjectValue(envDSNetworkingAttrTypes, map[string]attr.Value{
		"type":                   types.StringValue(env.Config.Networking.Type),
		"allow_mcp_servers":      types.BoolValue(env.Config.Networking.AllowMCPServers),
		"allow_package_managers": types.BoolValue(env.Config.Networking.AllowPackageManagers),
		"allowed_hosts":          allowedHosts,
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	aptList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Apt)
	resp.Diagnostics.Append(diags...)
	cargoList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Cargo)
	resp.Diagnostics.Append(diags...)
	gemList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Gem)
	resp.Diagnostics.Append(diags...)
	goList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Go)
	resp.Diagnostics.Append(diags...)
	npmList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Npm)
	resp.Diagnostics.Append(diags...)
	pipList, diags := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Pip)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	packagesObj, diags := types.ObjectValue(envDSPackagesAttrTypes, map[string]attr.Value{
		"apt":   aptList,
		"cargo": cargoList,
		"gem":   gemList,
		"go":    goList,
		"npm":   npmList,
		"pip":   pipList,
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	configObj, diags := types.ObjectValue(envDSConfigAttrTypes, map[string]attr.Value{
		"type":       types.StringValue(string(env.Config.Type)),
		"networking": networkingObj,
		"packages":   packagesObj,
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Config = configObj

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
