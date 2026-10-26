// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// Emptying allowed_hosts of a limited network sends "allowed_hosts": []:
// the update endpoint keeps an omitted field ("on update, omitted fields
// preserve the existing value",
// https://platform.claude.com/docs/en/api/beta/environments/update), so
// leaving the empty list out kept the old hosts and failed the apply with an
// inconsistent result.
func TestEnvironmentResource_allowedHostsEmptied(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	limited := func(hosts string) string {
		return `  config = {
    networking = {
      type          = "limited"
      allowed_hosts = [` + hosts + `]
    }
    packages = { apt = [], cargo = [], gem = [], go = [], npm = [], pip = [] }
  }
`
	}
	expectHosts := func(hosts ...knownvalue.Check) []statecheck.StateCheck {
		return []statecheck.StateCheck{statecheck.ExpectKnownValue(environmentAddr,
			tfjsonpath.New("config").AtMapKey("networking").AtMapKey("allowed_hosts"), knownvalue.ListExact(hosts))}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig(limited(`"a.example.com"`)),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectHosts(knownvalue.StringExact("a.example.com")),
			},
			{
				Config:            environmentConfig(limited("")),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectHosts(),
			},
		},
	})

	packages := `"packages": {"apt": [], "cargo": [], "gem": [], "go": [], "npm": [], "pip": []}`
	srv.AssertJSONBodies(routeCreateEnvironment, `{"name": "test-environment", "config": {"type": "cloud",
		"networking": {"type": "limited", "allowed_hosts": ["a.example.com"]}, `+packages+`}}`)
	srv.AssertJSONBodies(routeUpdateEnvironment, `{"name": "test-environment", "config": {"type": "cloud",
		"networking": {"type": "limited", "allowed_hosts": []}, `+packages+`}}`)
	srv.assertRequestShape(t)
}
