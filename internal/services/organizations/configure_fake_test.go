// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/organizations"
)

// TestOrganizations_requireAdminKey: every resource and data source of the
// package refuses to run with the standard key alone, before any request.
func TestOrganizations_requireAdminKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_invite resource":              "resource \"anthropic_invite\" \"test\" {\n  email = \"a@example.com\"\n  role = \"user\"\n}",
		"anthropic_organization_member resource": "resource \"anthropic_organization_member\" \"test\" {\n  id = \"user_x\"\n  role = \"user\"\n}",
		"anthropic_organization":                 `data "anthropic_organization" "test" {}`,
		"anthropic_organization_member":          `data "anthropic_organization_member" "test" { id = "user_x" }`,
		"anthropic_organization_members":         `data "anthropic_organization_members" "test" {}`,
		"anthropic_invite":                       `data "anthropic_invite" "test" { id = "invite_x" }`,
		"anthropic_invites":                      `data "anthropic_invites" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newOrgFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+Admin\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestOrganizations_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestOrganizations_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_invite":              organizations.NewInviteResource(),
		"anthropic_organization_member": organizations.NewOrganizationMemberResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_organization":         organizations.NewOrganizationDataSource(),
		"anthropic_organization_member":  organizations.NewOrganizationMemberDataSource(),
		"anthropic_organization_members": organizations.NewOrganizationMembersDataSource(),
		"anthropic_invite":               organizations.NewInviteDataSource(),
		"anthropic_invites":              organizations.NewInvitesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
