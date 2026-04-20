# Tests for the anthropic_agent data source example.
# Verifies that the data source returns expected attributes for a freshly-created agent.

test {
  parallel = true
}

run "agent_data_source_returns_agent" {
  module {
    source = "./examples/data-sources/agent"
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
    condition     = output.agent.id == anthropic_agent.example.id
    error_message = "Expected the data source id to match the resource id."
  }
}
