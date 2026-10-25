// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const fakeAgentConfig = `
resource "anthropic_agent" "test" {
  name  = "out-of-band"
  model = "claude-sonnet-4-6"
}
`

// An agent deleted outside Terraform is dropped by the refresh and re-created
// by the next apply, instead of failing the refresh with a 404.
func TestAgentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	fake := newFakeAgentAPI(t)
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeAgentConfig,
				Check: resource.TestCheckResourceAttrWith("anthropic_agent.test", "id", func(id string) error {
					firstID = id
					return nil
				}),
			},
			{
				PreConfig: func() {
					fake.with(func(f *fakeAgentAPI) { delete(f.objects, firstID) })
				},
				Config: fakeAgentConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_agent.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttrWith("anthropic_agent.test", "id", func(id string) error {
					if id == firstID {
						return fmt.Errorf("id = %s, want a new agent", id)
					}
					return nil
				}),
			},
		},
	})
}

// Destroy succeeds when the agent is already gone: the archive answers 404
// although the refresh still found it (deleted between refresh and destroy,
// or destroy run with -refresh=false).
func TestAgentResource_destroyToleratesNotFound(t *testing.T) {
	fake := newFakeAgentAPI(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			got := fake.requestsTo(http.MethodPost, "/v1/agents/"+id+"/archive")
			if len(got) != 1 || got[0].status != http.StatusNotFound {
				return fmt.Errorf("archive of %s: got %+v, want one request answered 404", id, got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fakeAgentConfig,
				Check: resource.TestCheckResourceAttrWith("anthropic_agent.test", "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				PreConfig: func() {
					fake.with(func(f *fakeAgentAPI) { f.writeNotFound = true })
				},
				Config:  fakeAgentConfig,
				Destroy: true,
			},
		},
	})
}
