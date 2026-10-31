// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
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
// "the gateway denies any route not listed here by default".
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
	awsDeployments = platform.Unverified("The platform page and the 2026-10-14 release notes list scheduled " +
		"deployments, but the gateway's route table, which denies unlisted routes, does not list " +
		"/v1/deployments or /v1/deployment_runs. A deployment also needs its agent's execution_identity " +
		"to be an aws_role.")
)

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{
		{New: agents.NewAgentResource, AWS: supported},
		{New: apikeys.NewAPIKeyResource, AWS: awsAPIKeys},
		{New: deployments.NewDeploymentResource, AWS: awsDeployments},
		{New: environments.NewEnvironmentResource, AWS: supported},
		{New: federation.NewFederationIssuerResource, AWS: awsNoOAuth},
		{New: federation.NewFederationRuleResource, AWS: awsNoOAuth},
		{New: federation.NewFederationRuleWorkspaceResource, AWS: awsNoOAuth},
		{New: files.NewFileResource, AWS: supported},
		{New: memorystores.NewMemoryResource, AWS: supported},
		{New: memorystores.NewMemoryStoreResource, AWS: supported},
		{New: messages.NewMessageResource, AWS: supported},
		{New: organizations.NewInviteResource, AWS: awsMembers},
		{New: organizations.NewOrganizationMemberResource, AWS: awsMembers},
		{New: serviceaccounts.NewServiceAccountResource, AWS: awsNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspaceResource, AWS: awsNoOAuth},
		{New: skills.NewSkillResource, AWS: supported},
		{New: skills.NewSkillVersionResource, AWS: supported},
		{New: userprofiles.NewUserProfileResource, AWS: supported},
		{New: vaults.NewVaultResource, AWS: supported},
		{New: vaults.NewVaultCredentialResource, AWS: supported},
		{New: workspaces.NewWorkspaceResource, AWS: supported},
		{New: workspaces.NewWorkspaceMemberResource, AWS: awsWorkspaceMembers},
	}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: agents.NewAgentDataSource, AWS: supported},
		{New: apikeys.NewAPIKeyDataSource, AWS: awsAPIKeys},
		{New: apikeys.NewAPIKeysDataSource, AWS: awsAPIKeys},
		{New: agents.NewAgentsDataSource, AWS: supported},
		{New: messages.NewCountTokensDataSource, AWS: supported},
		{New: deployments.NewDeploymentRunsDataSource, AWS: awsDeployments},
		{New: environments.NewEnvironmentDataSource, AWS: supported},
		{New: environments.NewEnvironmentsDataSource, AWS: supported},
		{New: federation.NewFederationIssuersDataSource, AWS: awsNoOAuth},
		{New: federation.NewFederationRulesDataSource, AWS: awsNoOAuth},
		{New: federation.NewFederationIssuerDataSource, AWS: awsNoOAuth},
		{New: federation.NewFederationRuleDataSource, AWS: awsNoOAuth},
		{New: federation.NewFederationRuleWorkspacesDataSource, AWS: awsNoOAuth},
		{New: files.NewFileDataSource, AWS: supported},
		{New: files.NewFilesDataSource, AWS: supported},
		{New: memorystores.NewMemoryDataSource, AWS: supported},
		{New: memorystores.NewMemoriesDataSource, AWS: supported},
		{New: memorystores.NewMemoryStoreDataSource, AWS: supported},
		{New: memorystores.NewMemoryStoresDataSource, AWS: supported},
		{New: models.NewModelDataSource, AWS: supported},
		{New: models.NewModelsDataSource, AWS: supported},
		{New: organizations.NewOrganizationDataSource, AWS: awsAdminRoutes},
		{New: organizations.NewOrganizationMemberDataSource, AWS: awsMembers},
		{New: organizations.NewOrganizationMembersDataSource, AWS: awsMembers},
		{New: organizations.NewInviteDataSource, AWS: awsMembers},
		{New: organizations.NewInvitesDataSource, AWS: awsMembers},
		{New: serviceaccounts.NewServiceAccountsDataSource, AWS: awsNoOAuth},
		{New: serviceaccounts.NewServiceAccountDataSource, AWS: awsNoOAuth},
		{New: serviceaccounts.NewServiceAccountWorkspacesDataSource, AWS: awsNoOAuth},
		{New: skills.NewSkillDataSource, AWS: supported},
		{New: skills.NewSkillVersionDataSource, AWS: supported},
		{New: skills.NewSkillVersionsDataSource, AWS: supported},
		{New: skills.NewSkillsDataSource, AWS: supported},
		{New: userprofiles.NewUserProfileDataSource, AWS: supported},
		{New: userprofiles.NewUserProfilesDataSource, AWS: supported},
		{New: workspaces.NewWorkspaceDataSource, AWS: supported},
		{New: workspaces.NewWorkspaceMemberDataSource, AWS: awsWorkspaceMembers},
		{New: workspaces.NewWorkspaceMembersDataSource, AWS: awsWorkspaceMembers},
		{New: workspaces.NewWorkspaceRateLimitsDataSource, AWS: awsRateLimits},
		{New: workspaces.NewWorkspacesDataSource, AWS: supported},
	}
}
