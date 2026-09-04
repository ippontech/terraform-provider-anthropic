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
	"github.com/ippontech/terraform-provider-anthropic/internal/services/federation"
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
	AuthToken   types.String `tfsdk:"auth_token"`
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
			"auth_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An org:admin OAuth bearer token (`sk-ant-oat01-...`) for endpoints that reject API keys, " +
					"such as the Workload Identity Federation admin endpoints. " +
					"Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.",
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

	apiKey := resolveCredential(data.ApiKey, "ANTHROPIC_API_KEY")
	adminApiKey := resolveCredential(data.AdminApiKey, "ANTHROPIC_ADMIN_API_KEY")
	authToken := resolveCredential(data.AuthToken, "ANTHROPIC_AUTH_TOKEN")

	if apiKey == "" && adminApiKey == "" && authToken == "" {
		resp.Diagnostics.AddError(
			"Missing Credentials",
			"At least one credential must be configured: api_key (ANTHROPIC_API_KEY) for standard resources, "+
				"admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources, "+
				"or auth_token (ANTHROPIC_AUTH_TOKEN) for endpoints that require an org:admin OAuth bearer token.",
		)
		return
	}

	// Each client carries exactly the credential resolved above and nothing
	// the environment contributed on its own — see newSDKClient.
	pd := &providerdata.ProviderData{}
	if apiKey != "" {
		pd.Client = newSDKClient(option.WithAPIKey(apiKey))
	}
	if adminApiKey != "" {
		pd.AdminClient = admin.NewClient(adminApiKey)
	}
	if authToken != "" {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(option.WithAuthToken(authToken))}
	}

	resp.DataSourceData = pd
	resp.ResourceData = pd
}

// resolveCredential returns the credential to use for one authentication
// method: the provider argument when it is set, otherwise the environment
// variable. An unknown value (an unresolved reference at plan time) is treated
// as unset, so the environment still applies.
func resolveCredential(configValue types.String, envVar string) string {
	if !configValue.IsNull() && !configValue.IsUnknown() {
		return configValue.ValueString()
	}
	return os.Getenv(envVar)
}

// newSDKClient builds an SDK client that carries exactly the credential passed
// in, and nothing the environment contributed on its own.
//
// option.WithoutEnvironmentDefaults suppresses anthropic.DefaultClientOptions
// entirely, which matters for more than the credential headers. That chain has
// five sources: ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, the profile named by
// ANTHROPIC_PROFILE, env-var federation, and the fallback profile under
// ~/.anthropic. The first two set a header before our explicit option is
// applied, so with both variables exported — the normal case here — a client
// would send x-api-key *and* Authorization, and the endpoints behind each
// credential reject the other one. The three profile/federation sources go
// further: option.WithConfig applies the profile's non-credential settings
// unconditionally, so a profile left behind by `ant auth login` (which also
// makes itself the active profile) would silently override the base URL and
// stamp its workspace_id on every request — neither of which appears anywhere
// in the Terraform configuration.
//
// The provider resolves credentials itself, so the only environment variable
// still worth honouring is ANTHROPIC_BASE_URL, which the marker option also
// skips. It is read with an explicit emptiness check: an exported-but-empty
// value must not replace the SDK's production default with "".
func newSDKClient(credential option.RequestOption) *anthropic.Client {
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), credential}
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := anthropic.NewClient(opts...)

	return &client
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
		federation.NewFederationIssuersDataSource,
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
