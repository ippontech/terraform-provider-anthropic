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

// Switching archive_on_destroy on for an existing environment is an update
// in place of a local-only attribute: ModifyPlan keeps updated_at, so the
// update must not write to the API, which moves updated_at on every update
// (https://platform.claude.com/docs/en/api/beta/environments/update returns
// the environment with its new "updated_at"). The destroy then archives.
func TestEnvironmentResource_archiveOnDestroySwitchedOn(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	createdAt := "2026-01-01T00:00:01Z"
	expect := func(archiveOnDestroy bool) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("id"), knownvalue.StringExact(environmentID(1))),
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(createdAt)),
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("archive_on_destroy"), knownvalue.Bool(archiveOnDestroy)),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig("  description = \"kept\"\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(false),
			},
			{
				Config:            environmentConfig("  description = \"kept\"\n  archive_on_destroy = true\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: append(expect(true), statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.StringExact("kept"))),
			},
		},
	})

	srv.AssertCalled(routeUpdateEnvironment, 0)
	srv.AssertCalled(routeArchiveEnvironment, 1)
	srv.AssertCalled(routeDeleteEnvironment, 0)
	srv.assertRequestShape(t)
}
