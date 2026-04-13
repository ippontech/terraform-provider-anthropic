# Tests for the anthropic_count_tokens data source example.
# Verifies that the data source returns a positive token count.

test {
  parallel = true
}

run "count_tokens_data_source_returns_results" {
  module {
    source = "./examples/data-sources/count_tokens"
  }

  assert {
    condition     = output.simple_token_count > 0
    error_message = "Expected simple_token_count to be greater than zero."
  }

  assert {
    condition     = output.tokens_with_system > output.simple_token_count
    error_message = "Expected token count with system prompt to be greater than without."
  }

  assert {
    condition     = output.conversation_token_count > output.simple_token_count
    error_message = "Expected token count for multi-turn conversation to be greater than a single message."
  }
}
