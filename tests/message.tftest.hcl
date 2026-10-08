# Tests for the anthropic_message resource example.
# Verifies that the resource creates successfully and populates computed attributes.

test {
  parallel = true
}

run "message_resource_creates_successfully" {
  module {
    source = "../examples/resources/message"
  }

  assert {
    condition     = output.response != ""
    error_message = "Expected the message response content to be non-empty."
  }

  assert {
    condition     = anthropic_message.example.id != ""
    error_message = "Expected the message id to be non-empty."
  }

  assert {
    condition     = anthropic_message.example.stop_reason != ""
    error_message = "Expected the stop_reason to be non-empty."
  }

  assert {
    condition     = anthropic_message.example.input_tokens > 0
    error_message = "Expected input_tokens to be greater than 0."
  }

  assert {
    condition     = anthropic_message.example.output_tokens > 0
    error_message = "Expected output_tokens to be greater than 0."
  }
}

run "message_resource_new_parameters" {
  module {
    source = "../examples/resources/message"
  }

  assert {
    condition     = anthropic_message.with_thinking.thinking_tokens > 0
    error_message = "Expected thinking_tokens to be greater than 0 when thinking is enabled."
  }

  assert {
    condition     = anthropic_message.with_thinking.cache_creation_input_tokens >= 0 && anthropic_message.with_thinking.cache_read_input_tokens >= 0
    error_message = "Expected the cache token counts to be set."
  }

  assert {
    condition     = anthropic_message.with_thinking.stop_sequence == null
    error_message = "Expected stop_sequence to be null when no stop sequence was matched."
  }

  assert {
    condition     = anthropic_message.with_thinking.stop_details == null
    error_message = "Expected stop_details to be null for a non-refusal."
  }

  assert {
    condition     = output.structured_capital == "Paris"
    error_message = "Expected the structured output to contain the capital Paris."
  }

  assert {
    condition     = anthropic_message.structured.thinking_tokens == 0
    error_message = "Expected thinking_tokens to be 0 without thinking."
  }
}
