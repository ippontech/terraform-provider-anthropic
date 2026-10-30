// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/apikeys"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/claudetag"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/compliance"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/deployments"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/environments"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/externalkeys"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/federation"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/files"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/memorystores"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/messages"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/models"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizations"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizationwebhooks"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/plugins"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/rbac"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/serviceaccounts"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/skills"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/spendlimits"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/telemetrydestinations"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/tunnels"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/userprofiles"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/vaults"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/workspaces"
)

// registration is a resource or data source factory with who can call it and
// its availability on each cloud gateway platform. Every type is available
// first party.
//
// The access and the availability sit next to the factory on purpose: a
// branch that adds a resource adds its factory here anyway, and declares on
// the same line which organization and credential it needs and whether each
// platform serves it. The registry tests fail on a type with no access, or no
// declaration for a platform a provider alias can be configured for.
type registration[T any] struct {
	New         func() T
	Access      access
	AWS         platform.Support
	GoogleCloud platform.Support
}

// access says who can call a type first party: the organization type whose
// credential its endpoints accept, and the provider argument that carries
// that credential. It changes nothing at run time: the provider index renders
// it as its credential table (TestIndexDocumentsCredentials).
type access struct {
	Organization string
	Credential   string
}

// Who can call each type. Sources: the "Auth" line of each type's page under
// templates/; User management, "Which endpoints can your organization use?"
// (members and invites for both organization types; groups, spend limits,
// plugins and Claude Tag for Claude Enterprise; workspaces, API keys, rate
// limits and the other endpoints of the Admin API guide, organization
// webhooks and service accounts among them, for Claude Console); the Admin
// API page ("From this page, only the members and invites endpoints apply"
// to Claude Enterprise); the CMEK Admin API page (external keys: "These calls
// work on Claude Platform"); the OpenTelemetry API page (destinations need
// "the admin role in a Claude Console organization"); the Compliance API
// access page (a Claude Enterprise organization turns the Compliance API on
// in claude.ai, a standalone Claude Console organization in the Console or
// through the Admin API); the spend limit set reference (Claude Console
// organizations set organization and workspace limits in an early access
// preview, while the Spend Limits API guide says Claude Enterprise only).
var (
	// Workspace APIs act in the workspace of the key, whatever the organization.
	workspaceKey = access{"Any workspace", "`api_key`"}
	// The Tunnels API is a workspace API that takes an OAuth token, no key.
	tunnelToken  = access{"Any workspace", "`auth_token` or `federation`: `workspace:manage_tunnels` scope"}
	consoleKey   = access{"Claude Console", "`admin_api_key`: Claude Console Admin API key"}
	consoleToken = access{"Claude Console", "`auth_token` or `federation`: `org:admin` scope"}
	// Claude Enterprise types need a scoped key, each with its own scopes.
	enterpriseKey = access{"Claude Enterprise", "`admin_api_key`: Claude Enterprise scoped key"}
	// Members and invites are the same endpoints for both organization types.
	eitherKey = access{"Claude Console or Claude Enterprise",
		"`admin_api_key`: Claude Console Admin API key or Claude Enterprise scoped key"}
	spendLimitKey = access{"Claude Enterprise, or Claude Console (early access preview)",
		"`admin_api_key`: Claude Console Admin API key or Claude Enterprise scoped key"}
)

// on returns the type's support on p.
func (r registration[T]) on(p platform.Platform) platform.Support {
	switch p {
	case platform.AWS:
		return r.AWS
	case platform.GoogleCloud:
		return r.GoogleCloud
	default:
		return platform.Supported()
	}
}

// Reasons a gateway platform does not serve a type, cited by the diagnostic.
// Sources, re-read on 2026-10-25: the Claude Platform on AWS page ("Features
// not supported") and its IAM actions page, whose route table states that
// "the gateway denies any route not listed here by default"; the Claude
// Models on Google Cloud (Anthropic managed) page and its IAM permissions
// page, whose 111 routes are the only ones with a permission.
var (
	supported = platform.Supported()

	awsAdminRoutesReason = "Its gateway serves only the workspace, external key and telemetry destination " +
		"routes of the Admin API."
	awsAdminRoutes = platform.Unavailable(awsAdminRoutesReason)
	awsMembers     = platform.Unavailable(awsAdminRoutesReason + " AWS IAM manages organization membership.")
	awsAPIKeys     = platform.Unavailable(awsAdminRoutesReason + " API keys for this platform are generated in the " +
		"AWS Console and are not Claude Console keys.")
	awsWorkspaceMembers = platform.Unavailable("Adding or removing users from individual workspaces is not " +
		"available: AWS IAM policies on workspace ARNs control access.")
	awsRateLimits = platform.Unavailable(awsAdminRoutesReason + " Rate limits are managed by Anthropic and shown " +
		"read-only in the Claude Console.")
	awsNoOAuth = platform.Unavailable("Its gateway accepts no OAuth token, only AWS SigV4 or a key generated in " +
		"the AWS Console, so Workload Identity Federation and its service accounts do not apply.")
	// Types added by PRs merged after the AWS review: not checked against
	// the gateway's route table yet, so neither refused nor promised.
	awsNotReviewed = platform.Unverified("Not reviewed for Claude Platform on AWS yet: the type was added after " +
		"the gateway's route table was checked.")
	gcAdminRoutesReason = "Its gateway serves the external key routes of the Admin API, not this type's; " +
		"Google Cloud IAM roles on the project control access."
	gcAdminRoutes = platform.Unavailable(gcAdminRoutesReason)
	gcMembers     = platform.Unavailable(gcAdminRoutesReason + " There are no organization members, invites or " +
		"workspace members to manage: the Claude Console is opened through Google Cloud IAM.")
	gcWorkspaces = platform.Unavailable("Workspaces are created, renamed and archived in the Claude Console only " +
		"(Settings > Workspaces): the gateway serves no workspace route. Read a workspace's ID there.")
	gcNoOAuth = platform.Unavailable("Authentication is through Google Cloud credentials only, with no API key and " +
		"no Anthropic OAuth token, so Workload Identity Federation and its service accounts do not apply.")
	gcUserProfiles = platform.Unverified("The platform pages and IAM permissions do not mention user profiles. The " +
		"SDK v1.84 sends their requests to the workspace endpoint, while SDK v2.3.0 sends them to the organization " +
		"endpoint.")
	// Types added by PRs merged after the Google Cloud review: not checked
	// yet, so neither refused nor promised.
	gcNotReviewed = platform.Unverified("Not reviewed for Claude Models on Google Cloud yet: the type was added " +
		"after the platform's routes were checked.")
	awsDeployments = platform.Unverified("The platform page and the 2026-10-14 release notes list scheduled " +
		"deployments, but the gateway's route table, which denies unlisted routes, does not list " +
		"/v1/deployments or /v1/deployment_runs. A deployment also needs its agent's execution_identity " +
		"to be an aws_role.")
)

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{
		{New: agents.NewAgentResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyResource, Access: consoleKey, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: claudetag.NewBundleTargetResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackChannelResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackWorkspaceSettingsResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: compliance.NewComplianceSettingsResource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: deployments.NewDeploymentResource, Access: workspaceKey, AWS: awsDeployments, GoogleCloud: supported},
		{New: environments.NewEnvironmentResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyResource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: federation.NewFederationIssuerResource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleResource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspaceResource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: messages.NewMessageResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewInviteResource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMemberResource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizationwebhooks.NewOrganizationWebhookResource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupMemberResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginInstallationSettingResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplaceSettingsResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginVersionResource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: serviceaccounts.NewServiceAccountResource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspaceResource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: telemetrydestinations.NewTelemetryDestinationResource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelResource, Access: tunnelToken, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelCertificateResource, Access: tunnelToken, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: spendlimits.NewSpendLimitResource, Access: spendLimitKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: userprofiles.NewUserProfileResource, Access: workspaceKey, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: vaults.NewVaultCredentialResource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: workspaces.NewWorkspaceResource, Access: consoleKey, AWS: supported, GoogleCloud: gcWorkspaces},
		{New: workspaces.NewWorkspaceMemberResource, Access: consoleKey, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitResource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
	}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: agents.NewAgentDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyDataSource, Access: consoleKey, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: apikeys.NewAPIKeysDataSource, Access: consoleKey, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: agents.NewAgentsDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: claudetag.NewBundleDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewBundlesDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackChannelDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackWorkspacesDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: messages.NewCountTokensDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: deployments.NewDeploymentRunsDataSource, Access: workspaceKey, AWS: awsDeployments, GoogleCloud: supported},
		{New: environments.NewEnvironmentDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: environments.NewEnvironmentsDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyDataSource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: externalkeys.NewExternalKeysDataSource, Access: consoleKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: federation.NewFederationIssuersDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRulesDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationIssuerDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspacesDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: files.NewFilesDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoriesDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoresDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelsDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewOrganizationDataSource, Access: consoleKey, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: organizations.NewOrganizationMemberDataSource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMembersDataSource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewInviteDataSource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewInvitesDataSource, Access: eitherKey, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: rbac.NewRBACGroupDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupsDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginsDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginVersionsDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplaceDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplacesDataSource, Access: enterpriseKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: serviceaccounts.NewServiceAccountsDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspacesDataSource, Access: consoleToken, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionsDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillsDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: supported},
		{New: tunnels.NewTunnelDataSource, Access: tunnelToken, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelsDataSource, Access: tunnelToken, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: userprofiles.NewUserProfileDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: userprofiles.NewUserProfilesDataSource, Access: workspaceKey, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultDataSource, Access: workspaceKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: vaults.NewVaultsDataSource, Access: workspaceKey, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: workspaces.NewWorkspaceDataSource, Access: consoleKey, AWS: supported, GoogleCloud: gcWorkspaces},
		{New: workspaces.NewWorkspaceMemberDataSource, Access: consoleKey, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceMembersDataSource, Access: consoleKey, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitsDataSource, Access: consoleKey, AWS: awsRateLimits, GoogleCloud: gcAdminRoutes},
		{New: workspaces.NewWorkspacesDataSource, Access: consoleKey, AWS: supported, GoogleCloud: gcWorkspaces},
	}
}
