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

// registration is a resource or data source factory with its availability on
// each cloud gateway platform. Every type is available first party.
//
// The availability sits next to the factory on purpose: a branch that adds a
// resource adds its factory here anyway, and declares on the same line whether
// each platform serves it. The registry tests fail on a type with no
// declaration for a platform a provider alias can be configured for.
type registration[T any] struct {
	New         func() T
	AWS         platform.Support
	GoogleCloud platform.Support
}

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
	// Types added by PRs merged after the Google Cloud review: not checked
	// yet, so neither refused nor promised.
	gcNotReviewed = platform.Unverified("Not reviewed for Claude Models on Google Cloud yet: the type was added " +
		"after the platform's routes were checked.")
	// Types added by PRs merged after the AWS review: not checked against
	// the gateway's route table yet, so neither refused nor promised.
	awsNotReviewed = platform.Unverified("Not reviewed for Claude Platform on AWS yet: the type was added after " +
		"the gateway's route table was checked.")
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
	awsDeployments = platform.Unverified("The platform page and the 2026-10-14 release notes list scheduled " +
		"deployments, but the gateway's route table, which denies unlisted routes, does not list " +
		"/v1/deployments or /v1/deployment_runs. A deployment also needs its agent's execution_identity " +
		"to be an aws_role.")
)

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{
		{New: agents.NewAgentResource, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyResource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: claudetag.NewBundleTargetResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackChannelResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackWorkspaceSettingsResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: compliance.NewComplianceSettingsResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: deployments.NewDeploymentResource, AWS: awsDeployments, GoogleCloud: supported},
		{New: environments.NewEnvironmentResource, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: federation.NewFederationIssuerResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspaceResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileResource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryResource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreResource, AWS: supported, GoogleCloud: supported},
		{New: messages.NewMessageResource, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewInviteResource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMemberResource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizationwebhooks.NewOrganizationWebhookResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginInstallationSettingResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplaceSettingsResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginVersionResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupMemberResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: serviceaccounts.NewServiceAccountResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspaceResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillResource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionResource, AWS: supported, GoogleCloud: supported},
		{New: spendlimits.NewSpendLimitResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: telemetrydestinations.NewTelemetryDestinationResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelCertificateResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: userprofiles.NewUserProfileResource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultCredentialResource, AWS: supported, GoogleCloud: supported},
		{New: vaults.NewVaultResource, AWS: supported, GoogleCloud: supported},
		{New: workspaces.NewWorkspaceMemberResource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitResource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: workspaces.NewWorkspaceResource, AWS: supported, GoogleCloud: gcWorkspaces},
	}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: agents.NewAgentDataSource, AWS: supported, GoogleCloud: supported},
		{New: agents.NewAgentsDataSource, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyDataSource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: apikeys.NewAPIKeysDataSource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: claudetag.NewBundleDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewBundlesDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackChannelDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: claudetag.NewSlackWorkspacesDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: deployments.NewDeploymentRunsDataSource, AWS: awsDeployments, GoogleCloud: supported},
		{New: environments.NewEnvironmentDataSource, AWS: supported, GoogleCloud: supported},
		{New: environments.NewEnvironmentsDataSource, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: externalkeys.NewExternalKeysDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: federation.NewFederationIssuerDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationIssuersDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRulesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspacesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileDataSource, AWS: supported, GoogleCloud: supported},
		{New: files.NewFilesDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoriesDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoresDataSource, AWS: supported, GoogleCloud: supported},
		{New: messages.NewCountTokensDataSource, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelDataSource, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelsDataSource, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewInviteDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewInvitesDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: organizations.NewOrganizationMemberDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMembersDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: plugins.NewPluginDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplaceDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginMarketplacesDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginsDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: plugins.NewPluginVersionsDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: rbac.NewRBACGroupsDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: serviceaccounts.NewServiceAccountDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountsDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspacesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillsDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionsDataSource, AWS: supported, GoogleCloud: supported},
		{New: tunnels.NewTunnelDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: tunnels.NewTunnelsDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: userprofiles.NewUserProfileDataSource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: userprofiles.NewUserProfilesDataSource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: vaults.NewVaultsDataSource, AWS: awsNotReviewed, GoogleCloud: gcNotReviewed},
		{New: workspaces.NewWorkspaceDataSource, AWS: supported, GoogleCloud: gcWorkspaces},
		{New: workspaces.NewWorkspaceMemberDataSource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceMembersDataSource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitsDataSource, AWS: awsRateLimits, GoogleCloud: gcAdminRoutes},
		{New: workspaces.NewWorkspacesDataSource, AWS: supported, GoogleCloud: gcWorkspaces},
	}
}
