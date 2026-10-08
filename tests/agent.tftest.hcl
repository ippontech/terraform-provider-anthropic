# Tests for the anthropic_agent data source example and the coordinator roster of the resource example.
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
}
