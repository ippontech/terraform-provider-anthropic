// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// defaultConfigJSON is the config the fake fills in on create as the
// provider sends it back on every update (packages without its type, which
// the schema does not hold).
const defaultConfigJSON = `{"type":"cloud","networking":{"type":"unrestricted"},` +
	`"packages":{"apt":[],"cargo":[],"gem":[],"go":[],"npm":[],"pip":[]}}`

// environmentUpdateBody is the body the provider sends to update the
// environment of environmentConfig, with the given JSON members.
func environmentUpdateBody(members string) string {
	return `{"name":"test-environment","config":` + defaultConfigJSON + `,` + members + `}`
}

// expectEnvironment checks the environment's description and metadata in
// state; "" and nil mean null.
func expectEnvironment(description string, metadata map[string]string) []statecheck.StateCheck {
	var desc knownvalue.Check = knownvalue.Null()
	if description != "" {
		desc = knownvalue.StringExact(description)
	}
	var meta knownvalue.Check = knownvalue.Null()
	if metadata != nil {
		values := map[string]knownvalue.Check{}
		for k, v := range metadata {
			values[k] = knownvalue.StringExact(v)
		}
		meta = knownvalue.MapExact(values)
	}
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("description"), desc),
		statecheck.ExpectKnownValue(environmentAddr, tfjsonpath.New("metadata"), meta),
	}
}

// expectEnvironmentAction checks the action planned for the environment
// before the apply, and an empty plan after the refresh that follows it.
func expectEnvironmentAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(environmentAddr, action)
}

// expectStoredEnvironment checks the one environment the fake holds.
func expectStoredEnvironment(srv *environmentsFake, description any, metadata map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		envs := srv.environments.List(nil)
		if len(envs) != 1 {
			return fmt.Errorf("the fake holds %d environments, want 1", len(envs))
		}
		if obj := envs[0]; !reflect.DeepEqual(obj["metadata"], metadata) || obj["description"] != description {
			return fmt.Errorf("stored description %#v, metadata %#v; want %#v, %#v", obj["description"], obj["metadata"], description, metadata)
		}
		return nil
	}
}

// Removing one metadata key deletes it server-side: the update sends it as
// null, since the API keeps omitted keys. `metadata = {}` then deletes the
// rest and stays an empty map in state.
func TestEnvironmentResource_metadataKeyRemoved(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig("  metadata = { team = \"a\", env = \"dev\" }\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectEnvironment("", map[string]string{"team": "a", "env": "dev"}),
			},
			{
				Config:            environmentConfig("  metadata = { team = \"a\" }\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectEnvironment("", map[string]string{"team": "a"}),
				Check:             expectStoredEnvironment(srv, nil, map[string]any{"team": "a"}),
			},
			{
				Config:            environmentConfig("  metadata = {}\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectEnvironment("", map[string]string{}),
				Check:             expectStoredEnvironment(srv, nil, map[string]any{}),
			},
		},
	})

	srv.AssertCalled(routeCreateEnvironment, 1)
	srv.AssertJSONBodies(routeUpdateEnvironment,
		environmentUpdateBody(`"metadata":{"team":"a","env":null}`),
		environmentUpdateBody(`"metadata":{"team":null}`))
	srv.assertRequestShape(t)
}

// Removing description and metadata from the configuration clears them: the
// plan shows the update (they used to be Optional+Computed, kept from state
// without a diff) and the update sends description null and every key null.
func TestEnvironmentResource_descriptionAndMetadataRemoved(t *testing.T) {
	t.Parallel()
	srv := newEnvironmentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkEnvironmentsDeleted(srv),
		Steps: []resource.TestStep{
			{
				Config:            environmentConfig("  description = \"to be removed\"\n  metadata    = { team = \"a\" }\n"),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectEnvironment("to be removed", map[string]string{"team": "a"}),
			},
			{
				Config:            environmentConfig(""),
				ConfigPlanChecks:  expectEnvironmentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectEnvironment("", nil),
				Check:             expectStoredEnvironment(srv, nil, map[string]any{}),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateEnvironment, environmentUpdateBody(`"description":null,"metadata":{"team":null}`))
	srv.assertRequestShape(t)
}
