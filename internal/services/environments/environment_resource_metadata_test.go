// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func environmentMetadataConfig(attrs string) string {
	return fmt.Sprintf(`
resource "anthropic_environment" "test" {
  name = "metadata"
%s
}
`, attrs)
}

// expectEnvironmentUpdate checks the body of the last update sent for the
// environment, field by field (nil means an explicit JSON null).
func expectEnvironmentUpdate(fake *fakeEnvironmentAPI, id *string, want map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		updates := fake.requestsTo(http.MethodPost, "/v1/environments/"+*id)
		if len(updates) == 0 {
			return fmt.Errorf("no update sent for %s", *id)
		}
		body := updates[len(updates)-1].body
		for field, v := range want {
			got, present := body[field]
			if !present || !reflect.DeepEqual(got, v) {
				return fmt.Errorf("update %s = %#v (present: %v), want %#v; body %v", field, got, present, v, body)
			}
		}
		return nil
	}
}

// expectStoredEnvironment checks the fake's server-side copy.
func expectStoredEnvironment(fake *fakeEnvironmentAPI, id *string, metadata map[string]any, description any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var err error
		fake.with(func(f *fakeEnvironmentAPI) {
			obj := f.objects[*id]
			if !reflect.DeepEqual(obj["metadata"], metadata) || obj["description"] != description {
				err = fmt.Errorf("stored metadata %v, description %#v; want %v, %#v", obj["metadata"], obj["description"], metadata, description)
			}
		})
		return err
	}
}

// Removing one metadata key deletes it server-side: the update sends it as
// null, since the API keeps omitted keys. `metadata = {}` then deletes the
// rest and stays an empty map in state.
func TestEnvironmentResource_metadataKeyRemoved(t *testing.T) {
	fake := newFakeEnvironmentAPI(t)
	const name = "anthropic_environment.test"
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: environmentMetadataConfig(`  metadata = { team = "a", env = "dev" }`),
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				Config: environmentMetadataConfig(`  metadata = { team = "a" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectEnvironmentUpdate(fake, &id, map[string]any{"metadata": map[string]any{"team": "a", "env": nil}}),
					expectStoredEnvironment(fake, &id, map[string]any{"team": "a"}, nil),
					resource.TestCheckResourceAttr(name, "metadata.%", "1"),
					resource.TestCheckResourceAttr(name, "metadata.team", "a"),
				),
			},
			{
				Config: environmentMetadataConfig(`  metadata = {}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectEnvironmentUpdate(fake, &id, map[string]any{"metadata": map[string]any{"team": nil}}),
					expectStoredEnvironment(fake, &id, map[string]any{}, nil),
					resource.TestCheckResourceAttr(name, "metadata.%", "0"),
				),
			},
		},
	})
}

// Removing description and metadata from the configuration clears them: the
// plan shows the update (they used to be Optional+Computed, kept from state
// without a diff) and the update sends description null and every key null.
func TestEnvironmentResource_descriptionAndMetadataRemoved(t *testing.T) {
	fake := newFakeEnvironmentAPI(t)
	const name = "anthropic_environment.test"
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: environmentMetadataConfig(`
  description = "to be removed"
  metadata    = { team = "a" }`),
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				Config: environmentMetadataConfig(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					expectEnvironmentUpdate(fake, &id, map[string]any{
						"description": nil,
						"metadata":    map[string]any{"team": nil},
					}),
					expectStoredEnvironment(fake, &id, map[string]any{}, nil),
					resource.TestCheckNoResourceAttr(name, "description"),
					resource.TestCheckNoResourceAttr(name, "metadata.%"),
				),
			},
		},
	})
}
