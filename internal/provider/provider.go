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
	ApiKey types.String `tfsdk:"api_key"`
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

	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"The Anthropic API key must be configured via the api_key provider argument or the ANTHROPIC_API_KEY environment variable.",
		)
		return
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	resp.DataSourceData = &client
	resp.ResourceData = &client
}

func (p *AnthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *AnthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewModelsDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AnthropicProvider{
			version: version,
		}
	}
}
