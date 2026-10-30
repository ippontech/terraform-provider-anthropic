// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// An agent archived outside Terraform is dropped by the refresh, as one
// deleted would be, and re-created by the next apply: the API keeps archived
// agents ("archived_at: When the agent was archived",
// https://platform.claude.com/docs/en/api/beta/agents/retrieve), but an
// archived agent cannot be used.
func TestAgentResource_archivedOutOfBandIsRecreated(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig(""),
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("id", str(agentID(1)))},
			},
			{
				PreConfig: func() {
					if _, ok := srv.agents.Archive(agentID(1)); !ok {
						t.Fatalf("%s is not in the fake", agentID(1))
					}
				},
				Config:            agentConfig(""),
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("id", str(agentID(2))), expectAttr("archived_at", null)},
			},
		},
	})

	srv.AssertCalled(routeCreateAgent, 2)
	srv.AssertCalled(routeUpdateAgent, 0)
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}

// API errors on each operation surface as error diagnostics naming the
// operation, and are not retried (4xx, https://platform.claude.com/docs/en/api/errors).
func TestAgentResource_apiErrors(t *testing.T) {
	t.Parallel()

	t.Run("create", func(t *testing.T) {
		t.Parallel()
		srv := newAgentsFake(t)
		srv.FailNext(routeCreateAgent, http.StatusBadRequest, 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:      agentConfig(""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+agent:.*400\s+Bad\s+Request`),
			}},
		})
		srv.AssertCalled(routeCreateAgent, 1)
		srv.AssertCalled(routeGetAgent, 0)
	})

	t.Run("read", func(t *testing.T) {
		t.Parallel()
		srv := newAgentsFake(t)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			CheckDestroy:             checkAgentsArchived(srv),
			Steps: []resource.TestStep{
				{Config: agentConfig(""), ConfigPlanChecks: expectAgentCreated},
				{
					PreConfig:   func() { srv.FailNext(routeGetAgent, http.StatusForbidden, 1) },
					Config:      agentConfig(""),
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+agent:.*403\s+Forbidden`),
				},
			},
		})
		srv.AssertCalled(routeUpdateAgent, 0)
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()
		srv := newAgentsFake(t)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			CheckDestroy:             checkAgentsArchived(srv),
			Steps: []resource.TestStep{
				{Config: agentConfig(""), ConfigPlanChecks: expectAgentCreated},
				{
					PreConfig:   func() { srv.FailNext(routeUpdateAgent, http.StatusBadRequest, 1) },
					Config:      agentConfig("  description = \"changed\"\n"),
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+agent:.*400\s+Bad\s+Request`),
				},
			},
		})
		srv.AssertCalled(routeUpdateAgent, 1)
	})

	t.Run("archive", func(t *testing.T) {
		t.Parallel()
		srv := newAgentsFake(t)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			CheckDestroy:             checkAgentsArchived(srv),
			Steps: []resource.TestStep{
				{Config: agentConfig(""), ConfigPlanChecks: expectAgentCreated},
				{
					PreConfig:   func() { srv.FailNext(routeArchiveAgent, http.StatusForbidden, 1) },
					Config:      agentConfig(""),
					Destroy:     true,
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+agent:.*403\s+Forbidden`),
				},
			},
		})
		// The failed archive, then the one of the final destroy.
		srv.AssertCalled(routeArchiveAgent, 2)
	})
}

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
