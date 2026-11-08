test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), and
# organization webhooks are a beta Anthropic turns on per organization, so
# this test only plans the public example with a dummy admin key. Configure
# only needs a non-empty credential and a create plan never calls the API.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "organization_webhook_plan" {
  command = plan

  module {
    source = "../examples/resources/organization_webhook"
  }

  assert {
    condition     = anthropic_organization_webhook.example.url == "https://example.com/anthropic-webhooks"
    error_message = "Expected url to be 'https://example.com/anthropic-webhooks'."
  }

  assert {
    condition     = anthropic_organization_webhook.example.name == "Enforcement actions"
    error_message = "Expected name to be 'Enforcement actions'."
  }

  assert {
    condition     = length(anthropic_organization_webhook.example.enabled_events) == 2
    error_message = "Expected both enforcement action event types."
  }

  assert {
    condition     = anthropic_organization_webhook.example.enabled == true
    error_message = "Expected enabled to default to true."
  }

  assert {
    condition     = anthropic_organization_webhook.example.signing_secret_version == 1
    error_message = "Expected signing_secret_version to be 1."
  }
}
