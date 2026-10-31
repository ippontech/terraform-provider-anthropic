// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"regexp"
	"sync/atomic"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure AnthropicProvider satisfies various provider interfaces.
var _ provider.Provider = &AnthropicProvider{}

// AnthropicProvider defines the provider implementation.
type AnthropicProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string

	// platform holds the platform.Platform that Configure resolved. The
	// resource and data source factories read it (see gate.go), on other
	// RPCs' goroutines, hence the atomic.
	platform atomic.Value
}

// AnthropicProviderModel describes the provider data model.
type AnthropicProviderModel struct {
	ApiKey      types.String `tfsdk:"api_key"`
	AdminApiKey types.String `tfsdk:"admin_api_key"`
	AuthToken   types.String `tfsdk:"auth_token"`
	Federation  types.Object `tfsdk:"federation"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	AWS         types.Object `tfsdk:"aws"`
}

func (p *AnthropicProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = providerTypeName
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
			"federation": federationSchema(),
			"workspace_id": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(workspaceIDPattern, "must be a workspace ID (`wrkspc_` followed by letters and digits)")},
				Description: "Workspace (`wrkspc_...`) the standard API key acts in, sent as the `anthropic-workspace-id` header " +
					"on every request of the standard client. Required with a personal or service account key that is " +
					"not scoped to a single workspace; must be left unset with a single-workspace key, which the API " +
					"rejects with a 404 when the header names any workspace, its own included. Not sent on the Admin API " +
					"or the org:admin OAuth client, which select their workspace otherwise. Has no environment fallback: " +
					"ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` only. " +
					"With the aws block: required, sent on every request except the Admin API ones, and read from " +
					"ANTHROPIC_AWS_WORKSPACE_ID when unset.",
			},
			"aws": awsSchema(),
		},
	}
}

func (p *AnthropicProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data AnthropicProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Unknown is an error, not unset, as for the federation block: reading
	// it as unset would send no header and route the plan's reads to the
	// key's own workspace, and the workspace is routinely a reference to a
	// resource of the same module.
	if data.WorkspaceID.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("workspace_id"), "Unknown Workspace Configuration",
			"workspace_id depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return
	}

	// A platform block, and only the block, selects a cloud gateway: the
	// variables that fill it are exported for other tools too. The alias then
	// has one SDK client and none of the first-party ones below.
	if !data.AWS.IsNull() {
		cfg, diags := resolveAWS(ctx, data)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		client, err := newAWSClient(ctx, *cfg)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("aws"), "Unable to Configure Claude Platform on AWS", err.Error())
			return
		}
		ignoredFirstPartyEnvironment(ctx, platform.AWS)
		addPlatformBetaWarning(&resp.Diagnostics, platform.AWS)
		p.serve(resp, providerdata.ForPlatform(platform.AWS, client))
		return
	}

	apiKey := resolveCredential(data.ApiKey, "ANTHROPIC_API_KEY")
	adminApiKey := resolveCredential(data.AdminApiKey, "ANTHROPIC_ADMIN_API_KEY")
	authToken := resolveCredential(data.AuthToken, "ANTHROPIC_AUTH_TOKEN")
	// Configuration only, deliberately: ANTHROPIC_WORKSPACE_ID is the SDKs'
	// federation variable, exported by other tools in the same job and by the
	// WIF guide for federation.workspace_id. A single-workspace key answers
	// 404 to any anthropic-workspace-id header, its own workspace included
	// (measured 2026-10-29), so an exported variable must never reach the
	// standard client on its own. Null reads as "".
	workspaceID := data.WorkspaceID.ValueString()

	federation, diags := resolveFederation(ctx, data.Federation)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Both would fill pd.OAuthClient, so configuring both is an error. The
	// check reads the configuration, not the resolved string: an auth_token
	// unknown at plan time is still set, and an exported ANTHROPIC_AUTH_TOKEN
	// gives way to the federation block, as the environment does to every
	// other provider argument.
	if federation != nil {
		if !data.AuthToken.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("federation"), "Conflicting OAuth Credentials",
				"federation and auth_token both supply the org:admin bearer token. Configure one.")
			return
		}
		// The SDKs give ANTHROPIC_AUTH_TOKEN precedence over their own
		// federation, so an operator used to them would expect the reverse.
		if authToken != "" {
			resp.Diagnostics.AddAttributeWarning(path.Root("federation"), "ANTHROPIC_AUTH_TOKEN Ignored",
				"ANTHROPIC_AUTH_TOKEN is set, but the federation block supplies the org:admin bearer token, so the variable is not used. "+
					"Unset it to silence this warning.")
			authToken = ""
		}
	}

	if apiKey == "" && adminApiKey == "" && authToken == "" && federation == nil {
		resp.Diagnostics.AddError(
			"Missing Credentials",
			"At least one credential must be configured: api_key (ANTHROPIC_API_KEY) for standard resources, "+
				"admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources, "+
				"or auth_token (ANTHROPIC_AUTH_TOKEN) or a federation block for endpoints that require an org:admin OAuth bearer token. "+
				"For an organization on Claude Platform on AWS, set the aws block instead.",
		)
		return
	}

	// Each client carries exactly the credential resolved above and nothing
	// the environment contributed on its own — see newSDKClient.
	pd := &providerdata.ProviderData{Platform: platform.FirstParty}
	if apiKey != "" {
		// Only the standard client is workspace-scoped by header: the Admin
		// API is organization-wide, and a federated bearer selects its
		// workspace at exchange time.
		opts := []option.RequestOption{option.WithAPIKey(apiKey)}
		if workspaceID != "" {
			opts = append(opts, option.WithHeader("anthropic-workspace-id", workspaceID))
		}
		pd.Client = newSDKClient(opts...)
	}
	if adminApiKey != "" {
		pd.AdminClient = admin.NewClient(adminApiKey)
	}
	if authToken != "" {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(option.WithAuthToken(authToken))}
	}
	if federation != nil {
		// The SDK caches the exchanged token and exchanges again shortly
		// before it expires, calling the identity source each time.
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(
			option.WithFederationTokenProvider(federation.identity, federation.options),
		)}
	}

	p.serve(resp, pd)
}

// serve hands pd to every resource and data source, and records its platform
// for the factory gate (gate.go).
func (p *AnthropicProvider) serve(resp *provider.ConfigureResponse, pd *providerdata.ProviderData) {
	p.platform.Store(pd.Platform)
	resp.DataSourceData = pd
	resp.ResourceData = pd
}

// workspaceIDPattern is the shape of a workspace ID. Checked at plan time so
// a stray newline (file() without trimspace) or a wrong ID never reaches the
// header, where net/http would reject the request on every call.
var workspaceIDPattern = regexp.MustCompile(`^wrkspc_[0-9A-Za-z]+$`)

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

// newSDKClient builds an SDK client that carries exactly the options passed
// in (the credential, and for the standard client the workspace header), and
// nothing the environment contributed on its own.
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
func newSDKClient(explicit ...option.RequestOption) *anthropic.Client {
	opts := append([]option.RequestOption{option.WithoutEnvironmentDefaults()}, explicit...)
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := anthropic.NewClient(opts...)

	return &client
}

// Resources and DataSources serve the registrations in registry.go, each
// factory gated by the platform Configure resolves (gate.go).
func (p *AnthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	regs := resourceRegistrations()
	factories := make([]func() resource.Resource, 0, len(regs))
	for _, reg := range regs {
		factories = append(factories, p.gateResource(reg))
	}
	return factories
}

func (p *AnthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	regs := dataSourceRegistrations()
	factories := make([]func() datasource.DataSource, 0, len(regs))
	for _, reg := range regs {
		factories = append(factories, p.gateDataSource(reg))
	}
	return factories
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AnthropicProvider{
			version: version,
		}
	}
}
