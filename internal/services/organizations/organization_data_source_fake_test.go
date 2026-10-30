// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const organizationConfig = `
data "anthropic_organization" "test" {}
`

// TestOrganizationDataSource_currentOrganization maps GET
// /v1/organizations/me, sent with the Admin key.
func TestOrganizationDataSource_currentOrganization(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	const addr = "data.anthropic_organization.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + organizationConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact("12345678-1234-5678-1234-567812345678")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact("Ippon Test Org")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("organization")),
			},
		}},
	})
	srv.assertAdminRequests(t)
	srv.AssertWalks(routeGetOrganization, "")
}

// TestOrganizationDataSource_apiError: an API error is an error diagnostic
// with the API's message.
func TestOrganizationDataSource_apiError(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.FailNext(routeGetOrganization, http.StatusUnauthorized, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + organizationConfig,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+organization:\s+API\s+error\s+\(401\s+authentication_error\)`),
		}},
	})
	srv.AssertCalled(routeGetOrganization, 1)
}
