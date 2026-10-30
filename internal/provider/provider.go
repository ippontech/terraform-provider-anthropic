// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"

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
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/apikeys"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/deployments"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/environments"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/federation"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/files"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/memorystores"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/messages"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/models"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizations"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/serviceaccounts"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/skills"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/userprofiles"
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
	ApiKey        types.String `tfsdk:"api_key"`
	AdminApiKey   types.String `tfsdk:"admin_api_key"`
	AuthToken     types.String `tfsdk:"auth_token"`
	Federation    types.Object `tfsdk:"federation"`
	APIFederation types.Object `tfsdk:"api_federation"`
	WorkspaceID   types.String `tfsdk:"workspace_id"`
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
					"ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` and `api_federation.workspace_id` only.",
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
	// Unknown is an error, not unset, as for the federation block: reading
	// it as unset would send no header and route the plan's reads to the
	// key's own workspace, and the workspace is routinely a reference to a
	// resource of the same module.
	if data.WorkspaceID.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("workspace_id"), "Unknown Workspace Configuration",
			"workspace_id depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return
	}
	// Configuration only, deliberately: ANTHROPIC_WORKSPACE_ID is the SDKs'
	// federation variable, exported by other tools in the same job and by the
	// WIF guide for federation.workspace_id. A single-workspace key answers
	// 404 to any anthropic-workspace-id header, its own workspace included
	// (measured 2026-10-29), so an exported variable must never reach the
	// standard client on its own. Null reads as "".
	workspaceID := data.WorkspaceID.ValueString()

	// The org:admin block passes no workspace: its token is organization-wide
	// and the provider-level workspace_id never reaches the OAuth client. The
	// standard client's block takes it as its exchange-time workspace.
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
				"federation needs an org:admin rule and api_federation a workspace:developer or workspace:inference rule. "+
				"Set a different federation_rule_id in one of them.", apiFederation.options.FederationRuleID))
	}
	if resp.Diagnostics.HasError() {
		return
	}

	if apiKey == "" && adminApiKey == "" && authToken == "" && federation == nil && apiFederation == nil {
		resp.Diagnostics.AddError(
			"Missing Credentials",
			"At least one credential must be configured: api_key (ANTHROPIC_API_KEY) or an api_federation block for standard resources, "+
				"admin_api_key (ANTHROPIC_ADMIN_API_KEY) for organization management resources, "+
				"or auth_token (ANTHROPIC_AUTH_TOKEN) or a federation block for endpoints that require an org:admin OAuth bearer token.",
		)
		return
	}

	// Each client carries exactly the credential resolved above and nothing
	// the environment contributed on its own — see newSDKClient.
	pd := &providerdata.ProviderData{}
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
	// The SDK caches each exchanged token and exchanges again shortly before
	// it expires, calling the identity source each time. The two blocks mint
	// two tokens, one per rule, since a rule carries a single scope.
	if federation != nil {
		pd.OAuthClient = &providerdata.OAuthClient{Client: newSDKClient(
			option.WithFederationTokenProvider(federation.identity, federation.options),
		)}
	}
	if apiFederation != nil {
		// No anthropic-workspace-id header: the minted token is already
		// workspace-scoped, and the SDK states the header does not apply to
		// federated tokens. The workspace was passed to the exchange above.
		pd.Client = newSDKClient(option.WithFederationTokenProvider(apiFederation.identity, apiFederation.options))
	}

	resp.DataSourceData = pd
	resp.ResourceData = pd
}

// federatedCredential names the credential a federation block replaces, for
// the diagnostics of rejectCredentialBeside.
type federatedCredential struct {
	block, argument, envVar, conflictSummary, what string
}

var (
	oauthFederationCredential = federatedCredential{
		block: "federation", argument: "auth_token", envVar: "ANTHROPIC_AUTH_TOKEN",
		conflictSummary: "Conflicting OAuth Credentials", what: "the org:admin bearer token",
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
// operator used to them would expect the reverse.
func (c federatedCredential) rejectCredentialBeside(diags *diag.Diagnostics, configured types.String, resolved string) {
	if !configured.IsNull() {
		diags.AddAttributeError(path.Root(c.block), c.conflictSummary,
			fmt.Sprintf("%s and %s both supply %s. Configure one.", c.block, c.argument, c.what))
		return
	}
	if resolved != "" {
		diags.AddAttributeWarning(path.Root(c.block), c.envVar+" Ignored",
			fmt.Sprintf("%s is set, but the %s block supplies %s, so the variable is not used. Unset it to silence this warning.",
				c.envVar, c.block, c.what))
	}
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

func (p *AnthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		agents.NewAgentResource,
		apikeys.NewAPIKeyResource,
		deployments.NewDeploymentResource,
		environments.NewEnvironmentResource,
		federation.NewFederationIssuerResource,
		federation.NewFederationRuleResource,
		federation.NewFederationRuleWorkspaceResource,
		files.NewFileResource,
		memorystores.NewMemoryResource,
		memorystores.NewMemoryStoreResource,
		messages.NewMessageResource,
		organizations.NewInviteResource,
		organizations.NewOrganizationMemberResource,
		serviceaccounts.NewServiceAccountResource,
		serviceaccounts.NewServiceAccountWorkspaceResource,
		skills.NewSkillResource,
		skills.NewSkillVersionResource,
		userprofiles.NewUserProfileResource,
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
		deployments.NewDeploymentRunsDataSource,
		environments.NewEnvironmentDataSource,
		environments.NewEnvironmentsDataSource,
		federation.NewFederationIssuersDataSource,
		federation.NewFederationRulesDataSource,
		federation.NewFederationIssuerDataSource,
		federation.NewFederationRuleDataSource,
		federation.NewFederationRuleWorkspacesDataSource,
		files.NewFileDataSource,
		files.NewFilesDataSource,
		memorystores.NewMemoryDataSource,
		memorystores.NewMemoriesDataSource,
		memorystores.NewMemoryStoreDataSource,
		memorystores.NewMemoryStoresDataSource,
		models.NewModelDataSource,
		models.NewModelsDataSource,
		organizations.NewOrganizationDataSource,
		organizations.NewOrganizationMemberDataSource,
		organizations.NewOrganizationMembersDataSource,
		organizations.NewInviteDataSource,
		organizations.NewInvitesDataSource,
		serviceaccounts.NewServiceAccountsDataSource,
		serviceaccounts.NewServiceAccountDataSource,
		serviceaccounts.NewServiceAccountWorkspacesDataSource,
		skills.NewSkillDataSource,
		skills.NewSkillVersionDataSource,
		skills.NewSkillVersionsDataSource,
		skills.NewSkillsDataSource,
		userprofiles.NewUserProfileDataSource,
		userprofiles.NewUserProfilesDataSource,
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
