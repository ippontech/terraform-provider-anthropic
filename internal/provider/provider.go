// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"regexp"
	"slices"
	"sync/atomic"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	provretry "github.com/ippontech/terraform-provider-anthropic/internal/retry"
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
	// workspaceless is set for a Google Cloud alias without a workspace,
	// which serves organization routes only (gate.go).
	workspaceless atomic.Bool

	// baseURL, when set by WithBaseURL, replaces the API host of every
	// client Configure builds. Empty in every binary users run.
	baseURL string
}

// Option changes how New builds the provider. Options are for tests only:
// main.go calls New without any, and none of them is reachable from a
// Terraform configuration.
type Option func(*AnthropicProvider)

// WithBaseURL points every client the provider builds at baseURL: the
// standard API key client, the Admin API client, the OAuth client and the
// federation token exchange behind it. It wins over ANTHROPIC_BASE_URL, so a
// test runs the provider against an httptest fake without t.Setenv and can
// call t.Parallel (internal/providertest wraps it).
func WithBaseURL(baseURL string) Option {
	return func(p *AnthropicProvider) {
		p.baseURL = baseURL
	}
}

// AnthropicProviderModel describes the provider data model.
type AnthropicProviderModel struct {
	ApiKey      types.String `tfsdk:"api_key"`
	AdminApiKey types.String `tfsdk:"admin_api_key"`
	AuthToken   types.String `tfsdk:"auth_token"`
	Federation  types.Object `tfsdk:"federation"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	AWS         types.Object `tfsdk:"aws"`
	GoogleCloud types.Object `tfsdk:"google_cloud"`
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
				Optional:  true,
				Sensitive: true,
				Description: "The key for the Admin API's organization endpoints (`/v1/organizations/...`): " +
					"a Claude Console Admin API key (`sk-ant-admin01-...`), a Claude Enterprise scoped Admin API key (`sk-ant-api01-...`, " +
					"for members and invites, groups, spend limits, plugins and Claude Tag, within its scopes), " +
					"or a personal or service account key not scoped to a workspace. The credential table of the provider " +
					"documentation says which resources need which. Sent in the `x-api-key` header. " +
					"Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.",
			},
			"auth_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An OAuth bearer token for endpoints that reject API keys: an org:admin token (`sk-ant-oat01-...`) " +
					"for the Workload Identity Federation admin endpoints, or a workspace:manage_tunnels token for MCP tunnels " +
					"(configure each in its own, aliased provider block). " +
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
					"ANTHROPIC_AWS_WORKSPACE_ID when unset. With the google_cloud block: part of " +
					"the request URL, never a header, required except for an alias limited to organization routes, and " +
					"read from ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID when unset.",
			},
			"aws":          awsSchema(),
			"google_cloud": googleCloudSchema(),
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

	if p.configurePlatform(ctx, data, resp) {
		return
	}

	creds, diags := resolveFirstPartyCredentials(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	p.serve(resp, p.firstPartyData(creds))
}

// configurePlatform configures an alias whose aws or google_cloud block is
// set, and reports whether one was: the alias then has one SDK client and
// none of the first-party ones. A platform block, and only the block,
// selects a cloud gateway: the variables that fill it are exported for other
// tools too.
func (p *AnthropicProvider) configurePlatform(ctx context.Context, data AnthropicProviderModel, resp *provider.ConfigureResponse) bool {
	switch {
	case !data.AWS.IsNull() && !data.GoogleCloud.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("google_cloud"), "Conflicting Platform Configuration",
			"aws and google_cloud cannot both be set: each reaches a different organization. Use a separate provider alias for each.")
	case !data.GoogleCloud.IsNull():
		p.configureGoogleCloud(ctx, data, resp)
	case !data.AWS.IsNull():
		p.configureAWS(ctx, data, resp)
	default:
		return false
	}
	return true
}

// configureGoogleCloud serves the single client of a Claude Models on Google
// Cloud alias.
func (p *AnthropicProvider) configureGoogleCloud(ctx context.Context, data AnthropicProviderModel, resp *provider.ConfigureResponse) {
	settings, diags := resolveGoogleCloud(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := newGoogleCloudClient(ctx, settings.config)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("google_cloud"), "Unable to Configure Claude Models on Google Cloud", err.Error())
		return
	}
	ignoredFirstPartyEnvironment(ctx, platform.GoogleCloud)
	addPlatformBetaWarning(&resp.Diagnostics, platform.GoogleCloud)
	p.workspaceless.Store(!settings.workspace)
	p.serve(resp, providerdata.ForPlatform(platform.GoogleCloud, client))
}

// configureAWS serves the single client of a Claude Platform on AWS alias.
func (p *AnthropicProvider) configureAWS(ctx context.Context, data AnthropicProviderModel, resp *provider.ConfigureResponse) {
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
}

// firstPartyCredentials are the credentials of a first-party alias, each
// from its provider argument or else its environment variable.
type firstPartyCredentials struct {
	apiKey, adminAPIKey, authToken, workspaceID string
	federation                                  *federationSettings
}

// resolveFirstPartyCredentials resolves the credentials of an alias without
// a platform block, and reports a federation block configured with
// auth_token, or no credential at all.
func resolveFirstPartyCredentials(ctx context.Context, data AnthropicProviderModel) (firstPartyCredentials, diag.Diagnostics) {
	creds := firstPartyCredentials{
		apiKey:      resolveCredential(data.ApiKey, "ANTHROPIC_API_KEY"),
		adminAPIKey: resolveCredential(data.AdminApiKey, "ANTHROPIC_ADMIN_API_KEY"),
		authToken:   resolveCredential(data.AuthToken, "ANTHROPIC_AUTH_TOKEN"),
		// Configuration only, deliberately: ANTHROPIC_WORKSPACE_ID is the SDKs'
		// federation variable, exported by other tools in the same job and by
		// the WIF guide for federation.workspace_id. A single-workspace key
		// answers 404 to any anthropic-workspace-id header, its own workspace
		// included (measured 2026-10-29), so an exported variable must never
		// reach the standard client on its own. Null reads as "".
		workspaceID: data.WorkspaceID.ValueString(),
	}

	federation, diags := resolveFederation(ctx, data.Federation)
	if diags.HasError() {
		return creds, diags
	}
	creds.federation = federation

	// Both would fill pd.OAuthClient, so configuring both is an error. The
	// check reads the configuration, not the resolved string: an auth_token
	// unknown at plan time is still set, and an exported ANTHROPIC_AUTH_TOKEN
	// gives way to the federation block, as the environment does to every
	// other provider argument.
	if federation != nil {
		if !data.AuthToken.IsNull() {
			diags.AddAttributeError(path.Root("federation"), "Conflicting OAuth Credentials",
				"federation and auth_token both supply the org:admin bearer token. Configure one.")
			return creds, diags
		}
		// The SDKs give ANTHROPIC_AUTH_TOKEN precedence over their own
		// federation, so an operator used to them would expect the reverse.
		if creds.authToken != "" {
			diags.AddAttributeWarning(path.Root("federation"), "ANTHROPIC_AUTH_TOKEN Ignored",
				"ANTHROPIC_AUTH_TOKEN is set, but the federation block supplies the org:admin bearer token, so the variable is not used. "+
					"Unset it to silence this warning.")
			creds.authToken = ""
		}
	}

	if creds.apiKey == "" && creds.adminAPIKey == "" && creds.authToken == "" && federation == nil {
		diags.AddError(
			"Missing Credentials",
			"At least one credential must be configured: api_key (ANTHROPIC_API_KEY) for standard resources, "+
				"admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources, "+
				"or auth_token (ANTHROPIC_AUTH_TOKEN) or a federation block for endpoints that require an org:admin OAuth bearer token. "+
				"For an organization on Claude Platform on AWS or Claude Models on Google Cloud, set the aws or "+
				"google_cloud block instead.",
		)
	}
	return creds, diags
}

// firstPartyData builds the first-party clients of creds. Each client
// carries exactly its credential and nothing the environment contributed on
// its own — see newSDKClient.
func (p *AnthropicProvider) firstPartyData(creds firstPartyCredentials) *providerdata.ProviderData {
	pd := &providerdata.ProviderData{Platform: platform.FirstParty}
	var override []option.RequestOption
	if p.baseURL != "" {
		override = append(override, option.WithBaseURL(p.baseURL))
	}
	if creds.apiKey != "" {
		// Only the standard client is workspace-scoped by header: the Admin
		// API is organization-wide, and a federated bearer selects its
		// workspace at exchange time.
		standard := slices.Clone(override)
		if creds.workspaceID != "" {
			standard = append(standard, option.WithHeader("anthropic-workspace-id", creds.workspaceID))
		}
		pd.Client = newSDKClient(option.WithAPIKey(creds.apiKey), standard...)
	}
	if creds.adminAPIKey != "" {
		pd.AdminClient = admin.NewClient(creds.adminAPIKey)
		if p.baseURL != "" {
			pd.AdminClient.BaseURL = p.baseURL
		}
	}
	if creds.authToken != "" {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(option.WithAuthToken(creds.authToken), override...)}
	}
	if creds.federation != nil {
		// The SDK caches the exchanged token and exchanges again shortly
		// before it expires, calling the identity source each time.
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(
			option.WithFederationTokenProvider(creds.federation.identity, creds.federation.options), override...,
		)}
	}
	return pd
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
//
// Retries follow provretry.DefaultPolicy instead of the SDK's: a POST that
// creates something, sends a message or updates with a precondition is
// replayed only on 429, 529 or a connection error before it got a
// connection, so another 5xx or a dropped connection after a write that
// landed cannot create a duplicate.
//
// override is applied last, so the base URL of WithBaseURL wins over the
// variable.
func newSDKClient(credential option.RequestOption, override ...option.RequestOption) *anthropic.Client {
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), credential}
	opts = append(opts, provretry.SDKOptions(provretry.DefaultPolicy)...)
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	opts = append(opts, override...)

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

func New(version string, opts ...Option) func() provider.Provider {
	return func() provider.Provider {
		p := &AnthropicProvider{
			version: version,
		}
		for _, opt := range opts {
			opt(p)
		}
		return p
	}
}
