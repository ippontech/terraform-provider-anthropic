// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func agentMetadataConfig(metadata string) string {
	return fmt.Sprintf(`
resource "anthropic_agent" "test" {
  name  = "metadata"
  model = "claude-sonnet-4-6"
%s
}
`, metadata)
}

// expectAgentMetadataUpdate checks the metadata sent by the last update of the
// agent (nil values are explicit JSON nulls) and the fake's stored copy.
func expectAgentMetadataUpdate(fake *fakeAgentAPI, id *string, sent, stored map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		updates := fake.requestsTo(http.MethodPost, "/v1/agents/"+*id)
		if len(updates) == 0 {
			return fmt.Errorf("no update sent for %s", *id)
		}
		if got := updates[len(updates)-1].body["metadata"]; !reflect.DeepEqual(got, sent) {
			return fmt.Errorf("update metadata = %#v, want %#v", got, sent)
		}
		var err error
		fake.with(func(f *fakeAgentAPI) {
			if got := f.objects[*id]["metadata"]; !reflect.DeepEqual(got, stored) {
				err = fmt.Errorf("stored metadata = %#v, want %#v", got, stored)
			}
		})
		return err
	}
}

// Removing one metadata key deletes it server-side: the update sends it as
// null, since the API keeps omitted keys. `metadata = {}` then deletes the
// rest and stays an empty map in state.
func TestAgentResource_metadataKeyRemoved(t *testing.T) {
	fake := newFakeAgentAPI(t)
	const name = "anthropic_agent.test"
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: agentMetadataConfig(`  metadata = { team = "a", env = "dev" }`),
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				Config: agentMetadataConfig(`  metadata = { team = "a" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectAgentMetadataUpdate(fake, &id,
						map[string]any{"team": "a", "env": nil},
						map[string]any{"team": "a"}),
					resource.TestCheckResourceAttr(name, "metadata.%", "1"),
					resource.TestCheckResourceAttr(name, "metadata.team", "a"),
				),
			},
			{
				Config: agentMetadataConfig(`  metadata = {}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectAgentMetadataUpdate(fake, &id, map[string]any{"team": nil}, map[string]any{}),
					resource.TestCheckResourceAttr(name, "metadata.%", "0"),
				),
			},
		},
	})
}

// Removing the metadata attribute deletes every key: the update used to send
// {}, which the API treats as "no change".
func TestAgentResource_metadataRemoved(t *testing.T) {
	fake := newFakeAgentAPI(t)
	const name = "anthropic_agent.test"
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: agentMetadataConfig(`  metadata = { team = "a", env = "dev" }`),
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				Config: agentMetadataConfig(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectAgentMetadataUpdate(fake, &id,
						map[string]any{"team": nil, "env": nil},
						map[string]any{}),
					resource.TestCheckNoResourceAttr(name, "metadata.%"),
				),
			},
		},
	})
}

// Clearing multiagent and removing a metadata key in the same update sends
// both: the two travel as extra fields, set in one SetExtraFields call
// because a second call would replace the first.
func TestAgentResource_metadataAndMultiagentClearedTogether(t *testing.T) {
	fake := newFakeAgentAPI(t)
	const name = "anthropic_agent.test"
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: agentMetadataConfig(`
  metadata   = { team = "a", env = "dev" }
  multiagent = {
    type   = "coordinator"
    agents = [{ type = "advisor", model = "claude-opus-5-5" }]
  }`),
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
					id = v
					return nil
				}),
			},
			{
				Config: agentMetadataConfig(`  metadata = { team = "a" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					expectAgentMetadataUpdate(fake, &id,
						map[string]any{"team": "a", "env": nil},
						map[string]any{"team": "a"}),
					func(*terraform.State) error {
						updates := fake.requestsTo(http.MethodPost, "/v1/agents/"+id)
						body := updates[len(updates)-1].body
						if v, present := body["multiagent"]; !present || v != nil {
							return fmt.Errorf("update multiagent = %#v (present: %v), want null", v, present)
						}
						return nil
					},
					resource.TestCheckNoResourceAttr(name, "multiagent.type"),
				),
			},
		},
	})
}
