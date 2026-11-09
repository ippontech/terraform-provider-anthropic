// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/apikeys"
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
	supported = platform.Supported()
	// gcOrganizationRoutes is a type Google Cloud serves whose requests all go
	// to /v1/organizations routes, so it also works on a provider alias that
	// sets no workspace.
	gcOrganizationRoutes = platform.Supported().OnOrganizationRoutes()

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
	awsNoOAuthReason = "Its gateway accepts no OAuth token, only AWS SigV4 or a key generated in the AWS Console"
	awsNoOAuth       = platform.Unavailable(awsNoOAuthReason + ", so Workload Identity Federation and its service " +
		"accounts do not apply.")
	awsTunnels = platform.Unavailable(awsNoOAuthReason + ", and the Tunnels API takes only an OAuth bearer token " +
		"with the workspace:manage_tunnels scope.")
	gcAdminRoutesReason = "Its gateway serves the external key routes of the Admin API, not this type's; " +
		"Google Cloud IAM roles on the project control access."
	gcAdminRoutes = platform.Unavailable(gcAdminRoutesReason)
	gcMembers     = platform.Unavailable(gcAdminRoutesReason + " There are no organization members, invites or " +
		"workspace members to manage: the Claude Console is opened through Google Cloud IAM.")
	gcWorkspaces = platform.Unavailable("Workspaces are created, renamed and archived in the Claude Console only " +
		"(Settings > Workspaces): the gateway serves no workspace route. Read a workspace's ID there.")
	gcNoOAuthReason = "Authentication is through Google Cloud credentials only, with no API key and no Anthropic " +
		"OAuth token"
	gcNoOAuth = platform.Unavailable(gcNoOAuthReason + ", so Workload Identity Federation and its service accounts " +
		"do not apply.")
	gcTunnels = platform.Unavailable(gcNoOAuthReason + ", and the Tunnels API takes only an OAuth bearer token " +
		"with the workspace:manage_tunnels scope.")
	gcUserProfiles = platform.Unverified("The platform pages and IAM permissions do not mention user profiles. The " +
		"SDK v1.84 sends their requests to the workspace endpoint, while SDK v2.3.0 sends them to the organization " +
		"endpoint.")
	gcTelemetry = platform.Unavailable("Telemetry destinations are not among its gateway's routes: the OpenTelemetry " +
		"documentation offers API tracing on the Claude API and Claude Platform on AWS only.")
)

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{
		{New: agents.NewAgentResource, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyResource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: compliance.NewComplianceSettingsResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		// Served on AWS, though the gateway's route table does not list them.
		{New: deployments.NewDeploymentResource, AWS: supported, GoogleCloud: supported},
		{New: environments.NewEnvironmentResource, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyResource, AWS: supported, GoogleCloud: gcOrganizationRoutes},
		{New: federation.NewFederationIssuerResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspaceResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileResource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryResource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreResource, AWS: supported, GoogleCloud: supported},
		{New: messages.NewMessageResource, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewInviteResource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMemberResource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizationwebhooks.NewOrganizationWebhookResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginInstallationSettingResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginMarketplaceSettingsResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginVersionResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: rbac.NewRBACGroupResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: rbac.NewRBACGroupMemberResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: serviceaccounts.NewServiceAccountResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspaceResource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillResource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionResource, AWS: supported, GoogleCloud: supported},
		{New: spendlimits.NewSpendLimitResource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: telemetrydestinations.NewTelemetryDestinationResource, AWS: supported, GoogleCloud: gcTelemetry},
		{New: tunnels.NewTunnelResource, AWS: awsTunnels, GoogleCloud: gcTunnels},
		{New: tunnels.NewTunnelCertificateResource, AWS: awsTunnels, GoogleCloud: gcTunnels},
		{New: userprofiles.NewUserProfileResource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultResource, AWS: supported, GoogleCloud: supported},
		{New: vaults.NewVaultCredentialResource, AWS: supported, GoogleCloud: supported},
		{New: workspaces.NewWorkspaceResource, AWS: supported, GoogleCloud: gcWorkspaces},
		{New: workspaces.NewWorkspaceMemberResource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitResource, AWS: awsRateLimits, GoogleCloud: gcAdminRoutes},
	}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: agents.NewAgentDataSource, AWS: supported, GoogleCloud: supported},
		{New: apikeys.NewAPIKeyDataSource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: apikeys.NewAPIKeysDataSource, AWS: awsAPIKeys, GoogleCloud: gcAdminRoutes},
		{New: agents.NewAgentsDataSource, AWS: supported, GoogleCloud: supported},
		{New: messages.NewCountTokensDataSource, AWS: supported, GoogleCloud: supported},
		{New: deployments.NewDeploymentRunsDataSource, AWS: supported, GoogleCloud: supported},
		{New: environments.NewEnvironmentDataSource, AWS: supported, GoogleCloud: supported},
		{New: environments.NewEnvironmentsDataSource, AWS: supported, GoogleCloud: supported},
		{New: externalkeys.NewExternalKeyDataSource, AWS: supported, GoogleCloud: gcOrganizationRoutes},
		{New: externalkeys.NewExternalKeysDataSource, AWS: supported, GoogleCloud: gcOrganizationRoutes},
		{New: federation.NewFederationIssuersDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRulesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationIssuerDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: federation.NewFederationRuleWorkspacesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: files.NewFileDataSource, AWS: supported, GoogleCloud: supported},
		{New: files.NewFilesDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoriesDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoreDataSource, AWS: supported, GoogleCloud: supported},
		{New: memorystores.NewMemoryStoresDataSource, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelDataSource, AWS: supported, GoogleCloud: supported},
		{New: models.NewModelsDataSource, AWS: supported, GoogleCloud: supported},
		{New: organizations.NewOrganizationDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: organizations.NewOrganizationMemberDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewOrganizationMembersDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewInviteDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: organizations.NewInvitesDataSource, AWS: awsMembers, GoogleCloud: gcMembers},
		{New: plugins.NewPluginDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginsDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginVersionsDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginMarketplaceDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: plugins.NewPluginMarketplacesDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: rbac.NewRBACGroupDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: rbac.NewRBACGroupsDataSource, AWS: awsAdminRoutes, GoogleCloud: gcAdminRoutes},
		{New: serviceaccounts.NewServiceAccountsDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspacesDataSource, AWS: awsNoOAuth, GoogleCloud: gcNoOAuth},
		{New: skills.NewSkillDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillVersionsDataSource, AWS: supported, GoogleCloud: supported},
		{New: skills.NewSkillsDataSource, AWS: supported, GoogleCloud: supported},
		{New: tunnels.NewTunnelCertificatesDataSource, AWS: awsTunnels, GoogleCloud: gcTunnels},
		{New: tunnels.NewTunnelDataSource, AWS: awsTunnels, GoogleCloud: gcTunnels},
		{New: tunnels.NewTunnelsDataSource, AWS: awsTunnels, GoogleCloud: gcTunnels},
		{New: userprofiles.NewUserProfileDataSource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: userprofiles.NewUserProfilesDataSource, AWS: supported, GoogleCloud: gcUserProfiles},
		{New: vaults.NewVaultDataSource, AWS: supported, GoogleCloud: supported},
		{New: vaults.NewVaultsDataSource, AWS: supported, GoogleCloud: supported},
		{New: workspaces.NewWorkspaceDataSource, AWS: supported, GoogleCloud: gcWorkspaces},
		{New: workspaces.NewWorkspaceMemberDataSource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceMembersDataSource, AWS: awsWorkspaceMembers, GoogleCloud: gcMembers},
		{New: workspaces.NewWorkspaceRateLimitsDataSource, AWS: awsRateLimits, GoogleCloud: gcAdminRoutes},
		{New: workspaces.NewWorkspacesDataSource, AWS: supported, GoogleCloud: gcWorkspaces},
	}
}
