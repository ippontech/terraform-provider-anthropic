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
	"github.com/ippontech/terraform-provider-anthropic/internal/provider/admin"
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

// ProviderData is passed to every resource and data source Configure call.
type ProviderData struct {
	// Client is the Anthropic SDK client for standard API endpoints.
	Client *anthropic.Client
	// AdminClient handles /v1/organizations/* endpoints using the Admin API key.
	// Nil when admin_api_key is not configured.
	AdminClient *admin.Client
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

	pd := &ProviderData{}
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
		NewAgentResource,
		NewEnvironmentResource,
		NewMessageResource,
		NewSkillResource,
		NewSkillVersionResource,
		NewWorkspaceResource,
	}
}

func (p *AnthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewAgentDataSource,
		NewAgentsDataSource,
		NewCountTokensDataSource,
		NewModelDataSource,
		NewModelsDataSource,
		NewSkillDataSource,
		NewSkillVersionDataSource,
		NewSkillVersionsDataSource,
		NewSkillsDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AnthropicProvider{
			version: version,
		}
	}
}
