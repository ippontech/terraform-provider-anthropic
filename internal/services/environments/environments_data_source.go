// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &EnvironmentsDataSource{}

func NewEnvironmentsDataSource() datasource.DataSource {
	return &EnvironmentsDataSource{}
}

type EnvironmentsDataSource struct {
	client *anthropic.Client
}

type EnvironmentsDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	Environments    types.List `tfsdk:"environments"`
}

var envListNetworkingAttrTypes = map[string]attr.Type{
	"type":                   types.StringType,
	"allow_mcp_servers":      types.BoolType,
	"allow_package_managers": types.BoolType,
	"allowed_hosts":          types.ListType{ElemType: types.StringType},
}

var envListPackagesAttrTypes = map[string]attr.Type{
	"apt":   types.ListType{ElemType: types.StringType},
	"cargo": types.ListType{ElemType: types.StringType},
	"gem":   types.ListType{ElemType: types.StringType},
	"go":    types.ListType{ElemType: types.StringType},
	"npm":   types.ListType{ElemType: types.StringType},
	"pip":   types.ListType{ElemType: types.StringType},
}

var envListConfigAttrTypes = map[string]attr.Type{
	"type":       types.StringType,
	"networking": types.ObjectType{AttrTypes: envListNetworkingAttrTypes},
	"packages":   types.ObjectType{AttrTypes: envListPackagesAttrTypes},
}

var envListItemAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"name":        types.StringType,
	"description": types.StringType,
	"metadata":    types.MapType{ElemType: types.StringType},
	"config":      types.ObjectType{AttrTypes: envListConfigAttrTypes},
	"archived_at": types.StringType,
	"created_at":  types.StringType,
	"updated_at":  types.StringType,
	"type":        types.StringType,
}

func (d *EnvironmentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environments"
}

func (d *EnvironmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all Anthropic cloud environments (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, archived environments are included in the results.",
			},
			"environments": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of environments.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique environment identifier (e.g. `env_...`).",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name for the environment.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Optional description of the environment.",
						},
						"metadata": schema.MapAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "User-provided metadata key-value pairs.",
						},
						"config": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Cloud environment configuration.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Environment configuration type. Always `cloud`.",
								},
								"networking": schema.SingleNestedAttribute{
									Computed:            true,
									MarkdownDescription: "Network configuration policy.",
									Attributes: map[string]schema.Attribute{
										"type": schema.StringAttribute{
											Computed:            true,
											MarkdownDescription: "Network policy type. One of `unrestricted` or `limited`.",
										},
										"allow_mcp_servers": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Permits outbound access to MCP server endpoints.",
										},
										"allow_package_managers": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Permits outbound access to public package registries.",
										},
										"allowed_hosts": schema.ListAttribute{
											Computed:            true,
											ElementType:         types.StringType,
											MarkdownDescription: "Specifies domains the container can reach.",
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
							MarkdownDescription: "Archive timestamp (RFC 3339). Null if the environment has not been archived.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Creation timestamp (RFC 3339).",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Last update timestamp (RFC 3339).",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `environment`.",
						},
					},
				},
			},
		},
	}
}

func (d *EnvironmentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *EnvironmentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data EnvironmentsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	listParams := anthropic.BetaEnvironmentListParams{}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		listParams.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}

	pager := d.client.Beta.Environments.ListAutoPaging(ctx, listParams)

	envObjs := make([]attr.Value, 0)
	for pager.Next() {
		env := pager.Current()

		allowedHosts, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Networking.AllowedHosts)
		resp.Diagnostics.Append(diags2...)

		networkingObj, diags2 := types.ObjectValue(envListNetworkingAttrTypes, map[string]attr.Value{
			"type":                   types.StringValue(env.Config.Networking.Type),
			"allow_mcp_servers":      types.BoolValue(env.Config.Networking.AllowMCPServers),
			"allow_package_managers": types.BoolValue(env.Config.Networking.AllowPackageManagers),
			"allowed_hosts":          allowedHosts,
		})
		resp.Diagnostics.Append(diags2...)

		aptList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Apt)
		resp.Diagnostics.Append(diags2...)
		cargoList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Cargo)
		resp.Diagnostics.Append(diags2...)
		gemList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Gem)
		resp.Diagnostics.Append(diags2...)
		goList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Go)
		resp.Diagnostics.Append(diags2...)
		npmList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Npm)
		resp.Diagnostics.Append(diags2...)
		pipList, diags2 := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Pip)
		resp.Diagnostics.Append(diags2...)

		packagesObj, diags2 := types.ObjectValue(envListPackagesAttrTypes, map[string]attr.Value{
			"apt":   aptList,
			"cargo": cargoList,
			"gem":   gemList,
			"go":    goList,
			"npm":   npmList,
			"pip":   pipList,
		})
		resp.Diagnostics.Append(diags2...)

		configObj, diags2 := types.ObjectValue(envListConfigAttrTypes, map[string]attr.Value{
			"type":       types.StringValue(string(env.Config.Type)),
			"networking": networkingObj,
			"packages":   packagesObj,
		})
		resp.Diagnostics.Append(diags2...)

		metaElems := make(map[string]attr.Value, len(env.Metadata))
		for k, v := range env.Metadata {
			metaElems[k] = types.StringValue(v)
		}
		metaMap, diags2 := types.MapValue(types.StringType, metaElems)
		resp.Diagnostics.Append(diags2...)

		archivedAt := types.StringNull()
		if env.ArchivedAt != "" {
			archivedAt = types.StringValue(env.ArchivedAt)
		}

		envObj, diags2 := types.ObjectValue(envListItemAttrTypes, map[string]attr.Value{
			"id":          types.StringValue(env.ID),
			"name":        types.StringValue(env.Name),
			"description": types.StringValue(env.Description),
			"metadata":    metaMap,
			"config":      configObj,
			"archived_at": archivedAt,
			"created_at":  types.StringValue(env.CreatedAt),
			"updated_at":  types.StringValue(env.UpdatedAt),
			"type":        types.StringValue(string(env.Type)),
		})
		resp.Diagnostics.Append(diags2...)

		envObjs = append(envObjs, envObj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list environments: %s", err))
		return
	}

	envList, diags := types.ListValue(types.ObjectType{AttrTypes: envListItemAttrTypes}, envObjs)
	resp.Diagnostics.Append(diags...)
	data.Environments = envList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
