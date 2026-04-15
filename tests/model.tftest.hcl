# Tests for the anthropic_model data source example.
# Verifies that the data source returns expected attributes for a known model.

test {
  parallel = true
}

run "model_data_source_returns_model" {
  module {
    source = "./examples/data-sources/model"
  }

  assert {
    condition     = output.model.id != ""
    error_message = "Expected the model id to be non-empty."
  }

  assert {
    condition     = output.model.display_name != ""
    error_message = "Expected the model display_name to be non-empty."
  }

  assert {
    condition     = output.model.created_at != ""
    error_message = "Expected the model created_at to be non-empty."
  }

  assert {
    condition     = output.model.max_input_tokens > 0
    error_message = "Expected max_input_tokens to be greater than zero."
  }

  assert {
    condition     = output.model.max_tokens > 0
    error_message = "Expected max_tokens to be greater than zero."
  }
}
