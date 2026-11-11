test {
  parallel = true
}

# Experimental resource on an API Anthropic does not document yet, verified on
# Claude Platform on AWS only: this test only plans the public example with a
# dummy key. Configure only needs a non-empty credential and a create plan
# never calls the API.
provider "anthropic" {
  api_key = "dummy-api-key"
}

run "webhook_plan" {
  command = plan

  module {
    source = "../examples/resources/webhook"
  }

  assert {
    condition     = anthropic_webhook.example.url == "https://example.com/anthropic-webhooks"
    error_message = "Expected url to be 'https://example.com/anthropic-webhooks'."
  }

  assert {
    condition     = anthropic_webhook.example.name == "Session status"
    error_message = "Expected name to be 'Session status'."
  }

  assert {
    condition     = length(anthropic_webhook.example.enabled_events) == 2
    error_message = "Expected both session event types."
  }

  assert {
    condition     = anthropic_webhook.example.enabled == true
    error_message = "Expected enabled to default to true."
  }

  assert {
    condition     = anthropic_webhook.example.signing_secret_version == 1
    error_message = "Expected signing_secret_version to be 1."
  }
}
