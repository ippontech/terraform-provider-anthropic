// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// An environment deleted outside Terraform is dropped by the refresh and
// re-created by the next apply, instead of failing the refresh with a 404.
func TestEnvironmentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	fake := newFakeEnvironmentAPI(t)
	const config = `
resource "anthropic_environment" "test" {
  name = "out-of-band"
}
`
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.TestCheckResourceAttrWith("anthropic_environment.test", "id", func(id string) error {
					firstID = id
					return nil
				}),
			},
			{
				PreConfig: func() {
					fake.with(func(f *fakeEnvironmentAPI) { delete(f.objects, firstID) })
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_environment.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttrWith("anthropic_environment.test", "id", func(id string) error {
					if id == firstID {
						return fmt.Errorf("id = %s, want a new environment", id)
					}
					return nil
				}),
			},
		},
	})
}

// Destroy succeeds when the environment is already gone: here the DELETE and
// the archive both answer 404 although the refresh still found the objects
// (deleted between refresh and destroy, or destroy run with -refresh=false).
func TestEnvironmentResource_destroyToleratesNotFound(t *testing.T) {
	fake := newFakeEnvironmentAPI(t)
	const config = `
resource "anthropic_environment" "deleted" {
  name = "deleted-on-destroy"
}

resource "anthropic_environment" "archived" {
  name               = "archived-on-destroy"
  archive_on_destroy = true
}
`
	ids := map[string]string{}
	recordID := func(name string) resource.TestCheckFunc {
		return resource.TestCheckResourceAttrWith("anthropic_environment."+name, "id", func(id string) error {
			ids[name] = id
			return nil
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, want := range []struct{ method, path string }{
				{http.MethodDelete, "/v1/environments/" + ids["deleted"]},
				{http.MethodPost, "/v1/environments/" + ids["archived"] + "/archive"},
			} {
				got := fake.requestsTo(want.method, want.path)
				if len(got) != 1 || got[0].status != http.StatusNotFound {
					return fmt.Errorf("%s %s: got %+v, want one request answered 404", want.method, want.path, got)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.ComposeTestCheckFunc(recordID("deleted"), recordID("archived")),
			},
			{
				PreConfig: func() {
					fake.with(func(f *fakeEnvironmentAPI) { f.writeNotFound = true })
				},
				Config:  config,
				Destroy: true,
			},
		},
	})
}
