// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func skill(typ, id, version string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"type": str(typ), "skill_id": str(id), "version": str(version)})
}

func customTool(name, description string, inputSchema knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"name": str(name), "description": str(description), "input_schema": inputSchema})
}

// The scalar attributes, the model options, skills, custom tools and the
// execution identity on create, update and removal:
//   - create sends every configured value, a skill without version is pinned
//     by the API and the pin lands in state;
//   - changing the model without model_effort sends no effort, removing
//     model_speed and model_inference_geo omits them (the API replaces the
//     model object), and removing description sends "" to clear it;
//   - a skill whose version is no longer configured is sent without one;
//   - an aws_role identity is sent, and removing it sends the service
//     account, which restores the default;
//   - removing skills and custom tools sends empty lists.
func TestAgentResource_attributes(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	const lookupSchema = `{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}`
	create := agentConfig(`  description         = "Answers questions."
  system              = "You are terse."
  model_speed         = "fast"
  model_effort        = "low"
  model_inference_geo = "us"
  skills = [
    { type = "anthropic", skill_id = "xlsx", version = "20251013" },
    { type = "custom", skill_id = "skill_01abc" },
  ]
  custom_tools = [
    {
      name         = "lookup"
      description  = "Looks a record up."
      input_schema = jsonencode({ type = "object", properties = { id = { type = "string" } }, required = ["id"], additionalProperties = false })
    },
    { name = "notify", description = "Sends a notification.", input_schema = jsonencode({ type = "object" }) },
  ]
`)
	update := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_agent" "test" {
  name   = "renamed-agent"
  model  = "claude-opus-4-6"
  system = "You are thorough."
  skills = [{ type = "anthropic", skill_id = "xlsx" }]
  custom_tools = [{
    name         = "lookup"
    description  = "Looks a record up."
    input_schema = jsonencode({ type = "object", properties = { id = { type = "string" } }, required = ["id"], additionalProperties = false })
  }]
  execution_identity = { type = "aws_role", role_arn = "arn:aws:iam::123456789012:role/agent-runner" }
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           create,
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("id", str(agentID(1))),
					expectAttr("name", str("test-agent")),
					expectAttr("model", str("claude-sonnet-4-6")),
					expectAttr("model_speed", str("fast")),
					expectAttr("model_effort", str("low")),
					expectAttr("model_inference_geo", str("us")),
					expectAttr("description", str("Answers questions.")),
					expectAttr("system", str("You are terse.")),
					expectAttr("metadata", null),
					expectAttr("skills", knownvalue.ListExact([]knownvalue.Check{
						skill("anthropic", "xlsx", "20251013"),
						skill("custom", "skill_01abc", "7"),
					})),
					expectAttr("custom_tools", knownvalue.ListExact([]knownvalue.Check{
						customTool("lookup", "Looks a record up.", str(lookupSchema)),
						customTool("notify", "Sends a notification.", str(`{"type":"object"}`)),
					})),
					expectAttr("execution_identity", null),
					expectAttr("multiagent", null),
					expectAttr("version", knownvalue.Int64Exact(1)),
					expectAttr("created_at", str("2026-01-01T00:00:01Z")),
					expectAttr("updated_at", str("2026-01-01T00:00:01Z")),
					expectAttr("archived_at", null),
				},
			},
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           update,
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("name", str("renamed-agent")),
					expectAttr("model", str("claude-opus-4-6")),
					expectAttr("model_speed", null),
					expectAttr("model_effort", str("low")),
					expectAttr("model_inference_geo", null),
					expectAttr("description", null),
					expectAttr("system", str("You are thorough.")),
					expectAttr("skills", knownvalue.ListExact([]knownvalue.Check{skill("anthropic", "xlsx", "7")})),
					expectAttr("custom_tools", knownvalue.ListExact([]knownvalue.Check{
						customTool("lookup", "Looks a record up.", str(lookupSchema)),
					})),
					expectAttr("execution_identity", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"type": str("aws_role"), "role_arn": str("arn:aws:iam::123456789012:role/agent-runner"),
					})),
					expectAttr("version", knownvalue.Int64Exact(2)),
					expectAttr("created_at", str("2026-01-01T00:00:01Z")),
					expectAttr("updated_at", str("2026-01-01T00:00:02Z")),
				},
			},
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("system", null),
					expectAttr("skills", null),
					expectAttr("custom_tools", null),
					expectAttr("execution_identity", null),
					expectAttr("version", knownvalue.Int64Exact(3)),
				},
			},
		},
	})

	const lookupBody = `{"type":"custom","name":"lookup","description":"Looks a record up.",` +
		`"input_schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}}`
	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","description":"Answers questions.","system":"You are terse.",
		"model":{"id":"claude-sonnet-4-6","speed":"fast","effort":"low","inference_geo":"us"},
		"skills":[{"type":"anthropic","skill_id":"xlsx","version":"20251013"},{"type":"custom","skill_id":"skill_01abc"}],
		"tools":[`+lookupBody+`,{"type":"custom","name":"notify","description":"Sends a notification.","input_schema":{"type":"object"}}]}`)
	srv.AssertJSONBodies(routeUpdateAgent,
		`{"name":"renamed-agent","description":"","system":"You are thorough.","version":1,"model":{"id":"claude-opus-4-6"},
		  "mcp_servers":[],"skills":[{"type":"anthropic","skill_id":"xlsx"}],"tools":[`+lookupBody+`],
		  "execution_identity":{"type":"aws_role","role_arn":"arn:aws:iam::123456789012:role/agent-runner"}}`,
		`{"name":"test-agent","description":"","system":"","version":2,"model":{"id":"claude-sonnet-4-6"},
		  "mcp_servers":[],"skills":[],"tools":[],"execution_identity":{"type":"service_account"}}`)
	srv.assertRequestShape(t)
}
