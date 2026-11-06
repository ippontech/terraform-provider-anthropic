test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), and a
# destination would export the organization's real traces, so this test only
# plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

variables {
  collector_api_key = "dummy-collector-api-key"
}

run "telemetry_destination_plan" {
  command = plan

  module {
    source = "../examples/resources/telemetry_destination"
  }

  assert {
    condition     = anthropic_telemetry_destination.example.endpoint_url == "https://gateway.example.com/otlp"
    error_message = "Expected endpoint_url to be 'https://gateway.example.com/otlp'."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.workspaces.type == "include"
    error_message = "Expected workspaces.type to be 'include'."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.traces.content_mode == "content"
    error_message = "Expected traces.content_mode to be 'content'."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.traces.enabled == true
    error_message = "Expected traces.enabled to default to true."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.enabled == true
    error_message = "Expected enabled to default to true."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.headers_wo == null
    error_message = "Expected the write-only headers_wo to be null in the plan."
  }

  assert {
    condition     = anthropic_telemetry_destination.example.headers_wo_version == 1
    error_message = "Expected headers_wo_version to be 1."
  }
}
