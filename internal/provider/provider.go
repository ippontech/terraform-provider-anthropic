// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/apikeys"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/environments"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/messages"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/models"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizations"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/skills"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/vaults"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/workspaces"
)

// Ensure AnthropicProvider satisfies various provider interfaces.
var _ provider.Provider = &AnthropicProvider{}

// AnthropicProvider defines the provider implementation.
type AnthropicProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// AnthropicProviderModel describes the provider data model.
type AnthropicProviderModel struct {
	ApiKey      types.String `tfsdk:"api_key"`
	AdminApiKey types.String `tfsdk:"admin_api_key"`
}

func (p *AnthropicProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "anthropic"
	resp.Version = p.version
}

func (p *AnthropicProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "The Anthropic API key. Can also be set via the ANTHROPIC_API_KEY environment variable.",
			},
			"admin_api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "The Anthropic Admin API key for organization management endpoints (workspaces, members). Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.",
			},
		},
	}
}

func (p *AnthropicProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data AnthropicProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if !data.ApiKey.IsNull() && !data.ApiKey.IsUnknown() {
		apiKey = data.ApiKey.ValueString()
	}

	adminApiKey := os.Getenv("ANTHROPIC_ADMIN_API_KEY")
	if !data.AdminApiKey.IsNull() && !data.AdminApiKey.IsUnknown() {
		adminApiKey = data.AdminApiKey.ValueString()
	}

	if apiKey == "" && adminApiKey == "" {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"At least one API key must be configured: api_key (ANTHROPIC_API_KEY) for standard resources, "+
				"or admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources.",
		)
		return
	}

	pd := &providerdata.ProviderData{}
	if apiKey != "" {
		client := anthropic.NewClient(option.WithAPIKey(apiKey))
		pd.Client = &client
	}
	if adminApiKey != "" {
		pd.AdminClient = admin.NewClient(adminApiKey)
	}

	resp.DataSourceData = pd
	resp.ResourceData = pd
}

func (p *AnthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		agents.NewAgentResource,
		apikeys.NewAPIKeyResource,
		environments.NewEnvironmentResource,
		messages.NewMessageResource,
		skills.NewSkillResource,
		skills.NewSkillVersionResource,
		vaults.NewVaultResource,
		vaults.NewVaultCredentialResource,
		workspaces.NewWorkspaceResource,
		workspaces.NewWorkspaceMemberResource,
	}
}

func (p *AnthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		agents.NewAgentDataSource,
		apikeys.NewAPIKeyDataSource,
		apikeys.NewAPIKeysDataSource,
		agents.NewAgentsDataSource,
		messages.NewCountTokensDataSource,
		environments.NewEnvironmentDataSource,
		environments.NewEnvironmentsDataSource,
		models.NewModelDataSource,
		models.NewModelsDataSource,
		organizations.NewOrganizationDataSource,
		organizations.NewOrganizationMemberDataSource,
		organizations.NewOrganizationMembersDataSource,
		skills.NewSkillDataSource,
		skills.NewSkillVersionDataSource,
		skills.NewSkillVersionsDataSource,
		skills.NewSkillsDataSource,
		workspaces.NewWorkspaceDataSource,
		workspaces.NewWorkspaceMemberDataSource,
		workspaces.NewWorkspaceMembersDataSource,
		workspaces.NewWorkspaceRateLimitsDataSource,
		workspaces.NewWorkspacesDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AnthropicProvider{
			version: version,
		}
	}
}
