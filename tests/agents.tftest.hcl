# Tests for the anthropic_agents data source example.
# Verifies that the data source returns a non-empty list of agents with expected attributes.

test {
  parallel = true
}

run "agents_data_source_returns_results" {
  module {
    source = "./examples/data-sources/agents"
  }

  assert {
    condition     = length(output.agents) > 0
    error_message = "Expected at least one Anthropic agent to be returned, but got none."
  }

  assert {
    condition     = output.agents[0].id != ""
    error_message = "Expected the first agent's id to be non-empty."
  }

  assert {
    condition     = output.agents[0].name != ""
    error_message = "Expected the first agent's name to be non-empty."
  }

  assert {
    condition     = output.agents[0].model != ""
    error_message = "Expected the first agent's model to be non-empty."
  }

  assert {
    condition     = output.agents[0].created_at != ""
    error_message = "Expected the first agent's created_at to be non-empty."
  }
}
