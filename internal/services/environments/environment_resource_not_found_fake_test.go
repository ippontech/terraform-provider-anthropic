// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// environmentID is the ID the fake gives to the n-th environment it creates.
func environmentID(n int) string { return fmt.Sprintf("env_%024d", n) }

// expectEnvironmentCreated checks that the step creates the environment and
// that the refresh after the apply plans nothing.
var expectEnvironmentCreated = providertest.ExpectAction(environmentAddr, plancheck.ResourceActionCreate)

// An environment deleted outside Terraform is dropped by the refresh and
// re-created from the configuration by the next apply, instead of failing the
// refresh with a 404.
func TestEnvironmentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	config := environmentConfig("  description = \"re-created after an out-of-band delete\"\n")
	expectEnvironment := func(id string) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
			statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), knownvalue.StringExact("re-created after an out-of-band delete")),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectEnvironmentCreated,
				ConfigStateChecks: expectEnvironment(environmentID(1)),
			},
			{
				PreConfig: func() {
					if !srv.environments.Delete(environmentID(1)) {
						t.Fatalf("%s is not in the fake", environmentID(1))
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectEnvironmentCreated,
				ConfigStateChecks: expectEnvironment(environmentID(2)),
			},
		},
	})

	// The refresh of step 2 met the 404: two creates, no update, and only the
	// second environment deleted by the final destroy.
	srv.AssertCalled(routeCreateEnvironment, 2)
	srv.AssertCalled(routeUpdateEnvironment, 0)
	srv.AssertCalled(routeDeleteEnvironment, 1)
	srv.assertRequestShape(t)
}

// Destroy succeeds when the environment is already gone: here the DELETE and
// the archive both answer 404 although the refresh still found the objects
// (deleted between refresh and destroy, or destroy run with -refresh=false).
func TestEnvironmentResource_destroyToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)
	config := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_environment" "deleted" {
  name = "deleted-on-destroy"
}

resource "anthropic_environment" "archived" {
  name               = "archived-on-destroy"
  archive_on_destroy = true
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_environment.deleted", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("anthropic_environment.archived", plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				PreConfig: func() {
					srv.FailNext(routeDeleteEnvironment, http.StatusNotFound, 1)
					srv.FailNext(routeArchiveEnvironment, http.StatusNotFound, 1)
				},
				Config:  config,
				Destroy: true,
			},
		},
	})

	// One DELETE and one archive, each answered 404 and not retried, on the
	// environment whose archive_on_destroy selects it.
	ids := map[string]string{}
	for _, id := range []string{environmentID(1), environmentID(2)} {
		obj, _ := srv.environments.Get(id)
		ids[fmt.Sprint(obj["name"])] = id
	}
	for route, want := range map[string]string{
		routeDeleteEnvironment:  "/v1/environments/" + ids["deleted-on-destroy"],
		routeArchiveEnvironment: "/v1/environments/" + ids["archived-on-destroy"] + "/archive",
	} {
		reqs := srv.Requests(route)
		if len(reqs) != 1 || reqs[0].Fault != "status 404" || reqs[0].Path != want {
			t.Errorf("%s: requests = %+v, want one to %s answered 404", route, reqs, want)
		}
	}
	srv.assertRequestShape(t)
}
