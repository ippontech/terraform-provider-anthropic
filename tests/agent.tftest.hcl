# Tests for the anthropic_agent data source example and the multiagent configurations of the resource example.
# Verifies that the data source returns expected attributes for a freshly-created agent.

test {
  parallel = true
}

run "agent_data_source_returns_agent" {
  module {
    source = "../examples/data-sources/agent"
  }

  assert {
    condition     = output.agent.id != ""
    error_message = "Expected the agent id to be non-empty."
  }

  assert {
    condition     = output.agent.name != ""
    error_message = "Expected the agent name to be non-empty."
  }

  assert {
    condition     = output.agent.model != ""
    error_message = "Expected the agent model to be non-empty."
  }

  assert {
    condition     = output.agent.version >= 1
    error_message = "Expected the agent version to be >= 1."
  }

  assert {
    condition     = output.agent.created_at != ""
    error_message = "Expected the agent created_at to be non-empty."
  }

  assert {
    condition     = output.agent.model_effort != null
    error_message = "Expected the API-resolved default model_effort to be mirrored by the data source."
  }

  assert {
    condition     = output.agent.id == anthropic_agent.example.id
    error_message = "Expected the data source id to match the resource id."
  }
}

run "agent_resource_example" {
  module {
    source = "../examples/resources/agent"
  }

  assert {
    condition     = output.simple_agent_model_effort == "high"
    error_message = "Expected model_effort to be high."
  }

  assert {
    condition     = output.coordinator_roster_size == 2
    error_message = "Expected the coordinator roster to hold the support agent and self."
  }

  assert {
    condition     = contains(anthropic_agent.developer.agent_toolset.configs[1].allowed_domains, "arxiv.org")
    error_message = "Expected web_search to keep its allowed_domains."
  }

  assert {
    condition     = anthropic_agent.developer.agent_toolset.configs[1].user_location.country == "US"
    error_message = "Expected web_search to keep its user_location."
  }

  assert {
    condition     = anthropic_agent.developer.agent_toolset.configs[2].max_content_tokens == 50000
    error_message = "Expected web_fetch to keep its max_content_tokens."
  }

  assert {
    condition     = anthropic_agent.developer.agent_toolset.configs[2].url_sources.client_tool_results.type == "none"
    error_message = "Expected web_fetch to keep its url_sources."
  }

  assert {
    condition     = contains(anthropic_agent.developer.agent_toolset.configs[2].url_sources.server_tool_results.tools, "web_search")
    error_message = "Expected web_fetch to keep its server_tool_results filter."
  }

  assert {
    condition     = anthropic_agent.mcp_agent.mcp_toolsets[0].default_permission_policy == "auto"
    error_message = "Expected the MCP toolset to keep the auto permission policy."
  }

  assert {
    condition     = anthropic_agent.mcp_agent.mcp_toolsets[0].configs[0].permission_policy == "always_ask"
    error_message = "Expected the delete_record override to keep always_ask."
  }

  assert {
    condition     = jsondecode(anthropic_agent.custom_tools.custom_tools[0].input_schema).additionalProperties == false
    error_message = "Expected the custom tool's input_schema to keep additionalProperties."
  }

  assert {
    condition     = anthropic_agent.coordinator.multiagent.type == "coordinator"
    error_message = "Expected a coordinator topology."
  }

  assert {
    condition     = anthropic_agent.coordinator.multiagent.agents[0].id == anthropic_agent.assistant.id
    error_message = "Expected the first roster entry to reference the assistant agent."
  }

  assert {
    condition     = anthropic_agent.coordinator.multiagent.agents[1].type == "self"
    error_message = "Expected the second roster entry to be self."
  }

  assert {
    condition     = anthropic_agent.orchestrator.multiagent.type == "multiagent_20261001"
    error_message = "Expected a multiagent_20261001 configuration."
  }

  assert {
    condition     = anthropic_agent.orchestrator.multiagent.subagents.predefined_agents[0].id == anthropic_agent.assistant.id
    error_message = "Expected the first predefined subagent to reference the assistant agent."
  }

  assert {
    condition     = anthropic_agent.orchestrator.multiagent.subagents.predefined_agents[1].type == "self"
    error_message = "Expected the second predefined subagent to be self."
  }

  assert {
    condition     = anthropic_agent.orchestrator.multiagent.workflows.enabled == false
    error_message = "Expected dynamic workflows to stay disabled."
  }
}
