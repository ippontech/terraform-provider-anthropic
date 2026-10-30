test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), and
# registering a key needs CMEK turned on plus a real KMS key, so this test only
# plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "external_key_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/external_key"
  }

  assert {
    condition     = anthropic_external_key.example.display_name == "prod-us-key"
    error_message = "Expected the AWS key display_name to be 'prod-us-key'."
  }

  assert {
    condition     = anthropic_external_key.example.geo == "us"
    error_message = "Expected the AWS key geo to be 'us'."
  }

  assert {
    condition     = anthropic_external_key.example.provider_config.type == "aws"
    error_message = "Expected the AWS key provider_config.type to be 'aws'."
  }

  assert {
    condition     = anthropic_external_key.gcp.provider_config.region == null
    error_message = "Expected provider_config.region to be planned null for a GCP key."
  }

  assert {
    condition     = anthropic_external_key.azure.provider_config.client_id == null
    error_message = "Expected the Azure key to use Anthropic's multitenant app (no client_id)."
  }
}
