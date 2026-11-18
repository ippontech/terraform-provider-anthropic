// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync/atomic"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/config"
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
	"github.com/hashicorp/terraform-plugin-log/tflog"
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
	// workspaceless is set for a Google Cloud alias without a workspace,
	// which serves organization routes only (gate.go).
	workspaceless atomic.Bool
}

// AnthropicProviderModel describes the provider data model.
type AnthropicProviderModel struct {
	ApiKey        types.String `tfsdk:"api_key"`
	AdminApiKey   types.String `tfsdk:"admin_api_key"`
	AuthToken     types.String `tfsdk:"auth_token"`
	Federation    types.Object `tfsdk:"federation"`
	APIFederation types.Object `tfsdk:"api_federation"`
	WorkspaceID   types.String `tfsdk:"workspace_id"`
	AWS           types.Object `tfsdk:"aws"`
	GoogleCloud   types.Object `tfsdk:"google_cloud"`
	CLI           types.Object `tfsdk:"cli"`
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
				Description: "The key for the Admin API's organization management endpoints (workspaces and their members, " +
					"workspace rate limits, API keys, the organization, organization members and invites): " +
					"a Claude Console Admin API key (`sk-ant-admin01-...`), a Claude Enterprise Admin API key with scopes (`sk-ant-api01-...`, members, invites, RBAC groups, spend limits, plugins and plugin marketplaces only), " +
					"or a personal or service account key not scoped to a workspace. Sent in the `x-api-key` header. " +
					"Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.",
			},
			"auth_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An org:admin OAuth bearer token (`sk-ant-oat01-...`) for endpoints that reject API keys, " +
					"such as the Workload Identity Federation admin endpoints, or a workspace:manage_tunnels one for MCP " +
					"tunnels (configure each in its own, aliased provider block). " +
					"Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.",
			},
			"federation":     federationSchema(),
			"api_federation": apiFederationSchema(),
			"workspace_id": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(workspaceIDPattern, "must be a workspace ID (`wrkspc_` followed by letters and digits)")},
				Description: "Workspace (`wrkspc_...`) the standard client acts in. With `api_key`, it is sent as the " +
					"`anthropic-workspace-id` header on every request of the standard client: required with a personal or " +
					"service account key that is not scoped to a single workspace; must be left unset with a single-workspace " +
					"key, which the API rejects with a 404 when the header names any workspace, its own included. With " +
					"`api_federation`, it scopes the token at exchange time instead (same as `api_federation.workspace_id`, " +
					"which must agree with it when both are set), and no header is sent. Not sent on the Admin API " +
					"or the org:admin OAuth client, which select their workspace otherwise. Has no environment fallback: " +
					"ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` and `api_federation.workspace_id` only. " +
					"With the aws block: required, sent on every request except the Admin API ones, and read from " +
					"ANTHROPIC_AWS_WORKSPACE_ID when unset. With the google_cloud block: part of " +
					"the request URL, never a header, required except for a configuration limited to organization routes, and " +
					"read from ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID when unset.",
			},
			"aws":          awsSchema(),
			"google_cloud": googleCloudSchema(),
			"cli":          cliSchema(),
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
	if !data.AWS.IsNull() && !data.GoogleCloud.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("google_cloud"), "Conflicting Platform Configuration",
			"aws and google_cloud cannot both be set: each reaches a different organization. Use a separate provider alias for each.")
		return
	}
	if !data.GoogleCloud.IsNull() {
		settings, diags := resolveGoogleCloud(ctx, data, googleTokenTimeout)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		client, err := newGoogleCloudClient(ctx, settings.config, googleTokenTimeout)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("google_cloud"), "Unable to Configure Claude Models on Google Cloud", err.Error())
			return
		}
		ignoredFirstPartyEnvironment(ctx, platform.GoogleCloud)
		addPlatformExperimentalWarning(&resp.Diagnostics, platform.GoogleCloud)
		p.workspaceless.Store(!settings.workspace)
		p.serve(resp, providerdata.ForPlatform(platform.GoogleCloud, client))
		return
	}
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
		addPlatformExperimentalWarning(&resp.Diagnostics, platform.AWS)
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

	// The OAuth block passes no workspace: an org:admin token is
	// organization-wide, a workspace:manage_tunnels one selects its workspace
	// through the rule, and the provider-level workspace_id never reaches the
	// OAuth client. The standard client's block takes it as its exchange-time
	// workspace.
	federation, diags := resolveFederation(ctx, "federation", data.Federation, "")
	resp.Diagnostics.Append(diags...)
	apiFederation, diags := resolveFederation(ctx, "api_federation", data.APIFederation, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Each block replaces one credential (auth_token, api_key), and both
	// would fill the same client: configuring both is an error, and the
	// credential's environment variable gives way to the block.
	if federation != nil {
		oauthFederationCredential.rejectCredentialBeside(&resp.Diagnostics, data.AuthToken, authToken)
		authToken = ""
	}
	if apiFederation != nil {
		apiFederationCredential.rejectCredentialBeside(&resp.Diagnostics, data.ApiKey, apiKey)
		apiKey = ""
	}
	// A rule carries a single scope, so one rule cannot back both clients:
	// one of them would hold a token of the wrong scope and fail on its
	// first request. Easy to hit with two empty blocks, since both fall back
	// to the same ANTHROPIC_FEDERATION_RULE_ID.
	if federation != nil && apiFederation != nil && federation.options.FederationRuleID == apiFederation.options.FederationRuleID {
		resp.Diagnostics.AddAttributeError(path.Root("api_federation").AtName("federation_rule_id"), "Conflicting Federation Rules",
			fmt.Sprintf("federation and api_federation both exchange against rule %s, but a rule grants a single scope: "+
				"federation needs a rule with the scope of the bearer this provider block uses (org:admin for the Workload Identity "+
				"Federation endpoints, workspace:manage_tunnels for MCP tunnels) and api_federation a workspace:developer or "+
				"workspace:inference rule. "+
				"Set a different federation_rule_id in one of them.", apiFederation.options.FederationRuleID))
	}
	// Each block exchanges on its own, so one file (or one literal, often
	// through ANTHROPIC_IDENTITY_TOKEN_FILE filling both) presents the same
	// assertion twice. That fails with jti_reused when the token carries a
	// jti, as GitHub, Kubernetes and Entra tokens do, but not otherwise, so
	// this is a warning: the second exchange would fail mid-apply, and the
	// fix is a command per block, which fetches a fresh token per exchange.
	if federation != nil && apiFederation != nil && federation.source.sharedWith(apiFederation.source) {
		resp.Diagnostics.AddAttributeWarning(path.Root("api_federation"), "Shared Identity Token Source",
			fmt.Sprintf("federation and api_federation read the same identity token %s, so both exchanges present the same assertion. "+
				"If the token carries a jti (GitHub Actions, Kubernetes and Entra tokens do), the second exchange fails with jti_reused. "+
				"Give each block its own identity_token_command, which fetches a fresh token per exchange.", federation.source.kind))
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// The SDK clients would refuse an unparsable ANTHROPIC_BASE_URL with an
	// error that quotes it, user information included. Only they read it: the
	// admin client has a fixed base URL, so an admin-only configuration skips
	// the check. It runs before resolveCLI, which compares the variable with
	// the profile's base_url and quotes it when they differ: stripping the
	// user information needs a parsable URL (#553).
	if apiKey != "" || authToken != "" || federation != nil || apiFederation != nil || !data.CLI.IsNull() {
		resp.Diagnostics.Append(checkBaseURL(path.Empty(), "", "", "ANTHROPIC_BASE_URL")...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// The cli block is the fallback for an operator's workstation: CI supplies
	// auth_token or federation, and the same configuration then never reads
	// a profile left on the runner.
	// The gate reads the configuration, as the federation check above does:
	// an auth_token unknown at plan time is still configured.
	var cliConfig *config.Config
	if !data.CLI.IsNull() {
		switch {
		case !data.AuthToken.IsNull():
			tflog.Info(ctx, "cli block not used: auth_token supplies the org:admin bearer token")
		case federation != nil:
			tflog.Info(ctx, "cli block not used: the federation block supplies the org:admin bearer token")
		case authToken != "":
			tflog.Info(ctx, "cli block not used: ANTHROPIC_AUTH_TOKEN is set and supplies the org:admin bearer token")
		}
	}
	if data.AuthToken.IsNull() && authToken == "" && federation == nil {
		cliConfig, diags = resolveCLI(ctx, data.CLI)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if apiKey == "" && adminApiKey == "" && authToken == "" && federation == nil && apiFederation == nil && cliConfig == nil {
		resp.Diagnostics.AddError(
			"Missing Credentials",
			"At least one credential must be configured: api_key (ANTHROPIC_API_KEY) or an api_federation block for standard resources, "+
				"admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources, "+
				"or auth_token (ANTHROPIC_AUTH_TOKEN), a federation block or a cli block for endpoints that require an OAuth bearer token: "+
				"org:admin scope for the Workload Identity Federation endpoints, workspace:manage_tunnels scope for MCP tunnels. "+
				"For an organization on Claude Platform on AWS or Claude Models on Google Cloud, set the aws or "+
				"google_cloud block instead.",
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
		pd.Client = newSDKClient(true, opts...)
	}
	if adminApiKey != "" {
		pd.AdminClient = admin.NewClient(adminApiKey)
	}
	if authToken != "" {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(false, option.WithAuthToken(authToken))}
	}
	// The SDK caches each exchanged token and exchanges again shortly before
	// it expires, calling the identity source each time. The two blocks mint
	// two tokens, one per rule, since a rule carries a single scope.
	if federation != nil {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(false,
			option.WithFederationTokenProvider(federation.identity, federation.options),
		)}
	}
	if apiFederation != nil {
		// No anthropic-workspace-id header: the minted token is already
		// workspace-scoped, and the SDK states the header does not apply to
		// federated tokens. The workspace was passed to the exchange above.
		// No Basic header either: the federation middleware sets Authorization.
		pd.Client = newSDKClient(false, option.WithFederationTokenProvider(apiFederation.identity, apiFederation.options))
	}
	if cliConfig != nil {
		// Quiet: the provider already rejects the static credentials the
		// SDK's shadowing warning is about.
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(false, option.WithConfigQuiet(cliConfig))}
	}

	p.serve(resp, pd)
}

// federatedCredential names the credential a federation block replaces, for
// the diagnostics of rejectCredentialBeside.
type federatedCredential struct {
	block, argument, envVar, conflictSummary, what string
}

var (
	oauthFederationCredential = federatedCredential{
		block: "federation", argument: "auth_token", envVar: "ANTHROPIC_AUTH_TOKEN",
		conflictSummary: "Conflicting OAuth Credentials", what: "the OAuth bearer token",
	}
	apiFederationCredential = federatedCredential{
		block: "api_federation", argument: "api_key", envVar: "ANTHROPIC_API_KEY",
		conflictSummary: "Conflicting API Credentials", what: "the standard client's credential",
	}
)

// rejectCredentialBeside applies the rule shared by the two federation blocks
// to the credential the block replaces: both would fill the same client, so
// configuring both is an error, and the credential's environment variable
// gives way to the block, with a warning.
//
// The conflict check reads the configuration, not the resolved string: a
// credential unknown at plan time is still set. The variable gives way as the
// environment does to every other provider argument; the warning is there
// because the SDKs rank the variable above their own federation, so an
// operator used to them would expect the reverse. It must not tell them to
// unset the variable: Configure cannot see the other provider blocks, and one
// without the federation block or the credential (the org:admin block next to
// a tunnels alias, a block with a plain api_key) still reads it.
func (c federatedCredential) rejectCredentialBeside(diags *diag.Diagnostics, configured types.String, resolved string) {
	if !configured.IsNull() {
		diags.AddAttributeError(path.Root(c.block), c.conflictSummary,
			fmt.Sprintf("%s and %s both supply %s. Configure one.", c.block, c.argument, c.what))
		return
	}
	if resolved != "" {
		diags.AddAttributeWarning(path.Root(c.block), c.envVar+" Ignored",
			fmt.Sprintf("%s is set, but this provider block takes %s from its %s block, so it does not use the variable. "+
				"A provider block with neither %s nor %s still does: keep the variable if another block relies on it, unset it otherwise.",
				c.envVar, c.what, c.block, c.block, c.argument))
	}
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
// Its user information (user:password@) is taken out of the URL, so that no
// request URL, and so no SDK error, carries the password
// (platform.BaseURLWithoutUserInfo). basicAuth says whether the credential
// leaves the Authorization header free: then, with an API key, the user
// information becomes the Basic header that net/http sent for it. A bearer
// token sets that header itself, and the SDK's federation middleware stands
// down when it finds one already set, so those clients get no Basic header,
// as net/http sent them none.
func newSDKClient(basicAuth bool, explicit ...option.RequestOption) *anthropic.Client {
	baseURL, credentials := platform.BaseURLWithoutUserInfo("", "ANTHROPIC_BASE_URL", basicAuth)
	opts := append([]option.RequestOption{option.WithoutEnvironmentDefaults()}, credentials...)
	opts = append(opts, explicit...)
	if baseURL != "" {
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
