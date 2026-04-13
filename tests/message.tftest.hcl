# Tests for the anthropic_message resource example.
# Verifies that the resource creates successfully and populates computed attributes.

test {
  parallel = true
}

run "message_resource_creates_successfully" {
  module {
    source = "./examples/resources/message"
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
