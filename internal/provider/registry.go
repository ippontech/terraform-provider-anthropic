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

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{
		{New: agents.NewAgentResource},
		{New: apikeys.NewAPIKeyResource},
		{New: deployments.NewDeploymentResource},
		{New: environments.NewEnvironmentResource},
		{New: federation.NewFederationIssuerResource},
		{New: federation.NewFederationRuleResource},
		{New: federation.NewFederationRuleWorkspaceResource},
		{New: files.NewFileResource},
		{New: memorystores.NewMemoryResource},
		{New: memorystores.NewMemoryStoreResource},
		{New: messages.NewMessageResource},
		{New: organizations.NewInviteResource},
		{New: organizations.NewOrganizationMemberResource},
		{New: serviceaccounts.NewServiceAccountResource},
		{New: serviceaccounts.NewServiceAccountWorkspaceResource},
		{New: skills.NewSkillResource},
		{New: skills.NewSkillVersionResource},
		{New: userprofiles.NewUserProfileResource},
		{New: vaults.NewVaultResource},
		{New: vaults.NewVaultCredentialResource},
		{New: workspaces.NewWorkspaceResource},
		{New: workspaces.NewWorkspaceMemberResource},
	}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: agents.NewAgentDataSource},
		{New: apikeys.NewAPIKeyDataSource},
		{New: apikeys.NewAPIKeysDataSource},
		{New: agents.NewAgentsDataSource},
		{New: messages.NewCountTokensDataSource},
		{New: deployments.NewDeploymentRunsDataSource},
		{New: environments.NewEnvironmentDataSource},
		{New: environments.NewEnvironmentsDataSource},
		{New: federation.NewFederationIssuersDataSource},
		{New: federation.NewFederationRulesDataSource},
		{New: federation.NewFederationIssuerDataSource},
		{New: federation.NewFederationRuleDataSource},
		{New: federation.NewFederationRuleWorkspacesDataSource},
		{New: files.NewFileDataSource},
		{New: files.NewFilesDataSource},
		{New: memorystores.NewMemoryDataSource},
		{New: memorystores.NewMemoriesDataSource},
		{New: memorystores.NewMemoryStoreDataSource},
		{New: memorystores.NewMemoryStoresDataSource},
		{New: models.NewModelDataSource},
		{New: models.NewModelsDataSource},
		{New: organizations.NewOrganizationDataSource},
		{New: organizations.NewOrganizationMemberDataSource},
		{New: organizations.NewOrganizationMembersDataSource},
		{New: organizations.NewInviteDataSource},
		{New: organizations.NewInvitesDataSource},
		{New: serviceaccounts.NewServiceAccountsDataSource},
		{New: serviceaccounts.NewServiceAccountDataSource},
		{New: serviceaccounts.NewServiceAccountWorkspacesDataSource},
		{New: skills.NewSkillDataSource},
		{New: skills.NewSkillVersionDataSource},
		{New: skills.NewSkillVersionsDataSource},
		{New: skills.NewSkillsDataSource},
		{New: userprofiles.NewUserProfileDataSource},
		{New: userprofiles.NewUserProfilesDataSource},
		{New: workspaces.NewWorkspaceDataSource},
		{New: workspaces.NewWorkspaceMemberDataSource},
		{New: workspaces.NewWorkspaceMembersDataSource},
		{New: workspaces.NewWorkspaceRateLimitsDataSource},
		{New: workspaces.NewWorkspacesDataSource},
	}
}
