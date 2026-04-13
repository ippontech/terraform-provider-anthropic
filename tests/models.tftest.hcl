# Tests for the anthropic_models data source example.
# Verifies that the data source returns a non-empty list of models with expected attributes.

test {
  parallel = true
}

run "models_data_source_returns_results" {
  module {
    source = "./examples/data-sources/models"
  }

  assert {
    condition     = length(output.models) > 0
    error_message = "Expected at least one Anthropic model to be returned, but got none."
  }

  assert {
    condition     = output.models[0].id != ""
    error_message = "Expected the first model's id to be non-empty."
  }

  assert {
    condition     = output.models[0].display_name != ""
    error_message = "Expected the first model's display_name to be non-empty."
  }

  assert {
    condition     = output.models[0].created_at != ""
    error_message = "Expected the first model's created_at to be non-empty."
  }
}
