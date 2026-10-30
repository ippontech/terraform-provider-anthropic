// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// An input_schema reaches the API as configured, with `type: object` added
// when it has no type (the attribute's documentation), and state keeps the
// configured value; a schema that is not a JSON object fails before any
// request. The request side used to decode the schema into the SDK struct,
// which drops a schema whose fields have other types ({"properties": 5}, a
// list) or which is empty, and the state side compared the echo with the
// configured value, so a schema without type failed the apply with
// "inconsistent result after apply".
func TestAgentResource_inputSchemaAsConfigured(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		schema, sent string
	}{
		"without type": {`{ properties = { id = { type = "string" } }, additionalProperties = false }`, `{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":false}`},
		"empty":        {`{}`, `{"type":"object"}`},
		// The API requires an input_schema: none configured sends a tool
		// without parameters, and the state keeps the attribute null.
		"omitted": {``, `{"type":"object"}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newAgentsFake(t)
			inputSchema := ``
			if tc.schema != `` {
				inputSchema = `, input_schema = jsonencode(` + tc.schema + `)`
			}
			config := agentConfig(`  custom_tools = [{ name = "lookup", description = "d"` + inputSchema + ` }]
`)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				CheckDestroy:             checkAgentsArchived(srv),
				Steps: []resource.TestStep{
					{Config: config, ConfigPlanChecks: expectAgentCreated},
					{ResourceName: agentAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"custom_tools.0.input_schema"}},
				},
			})
			srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},`+
				`"tools":[{"type":"custom","name":"lookup","description":"d","input_schema":`+tc.sent+`}]}`)
		})
	}

	t.Run("not an object", func(t *testing.T) {
		t.Parallel()
		srv := newAgentsFake(t)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      agentConfig("  custom_tools = [{ name = \"lookup\", description = \"d\", input_schema = jsonencode([\"id\"]) }]\n"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+input_schema.*"lookup"`),
			}},
		})
		srv.AssertCalled("", 0)
	})
}

// An imported custom tool whose schema the API returns as {"type": "object"}
// reads back as that schema: import has no current value to keep, so the
// state takes the API's JSON, which is what a configuration with
// input_schema = jsonencode({ type = "object" }) holds. Only a tool the
// current value holds without a schema (the "omitted" case above) keeps a
// null input_schema while the API returns {"type": "object"}.
func TestAgentResource_inputSchemaObjectImported(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig("  custom_tools = [{ name = \"lookup\", description = \"d\", input_schema = jsonencode({ type = \"object\" }) }]\n"),
				ConfigPlanChecks: expectAgentCreated,
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(agentAddr,
					tfjsonpath.New("custom_tools").AtSliceIndex(0).AtMapKey("input_schema"), knownvalue.StringExact(`{"type":"object"}`))},
			},
			{ResourceName: agentAddr, ImportState: true, ImportStateVerify: true},
		},
	})
	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},`+
		`"tools":[{"type":"custom","name":"lookup","description":"d","input_schema":{"type":"object"}}]}`)
}
