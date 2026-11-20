test {
  parallel = true
}

# CMEK must be turned on per organization and the example's key ID is a
# placeholder, so the provider is mocked and the public example runs end to
# end (plan + apply) with no credentials. override_data pins the computed
# values so the assertions prove the example wiring; provider logic is covered
# by the httptest unit tests.
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_external_key.example
    values = {
      attachment = "attached"
      provider_config = {
        type    = "aws"
        kms_arn = "arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222"
      }
    }
  }
}

run "external_key_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/external_key"
  }
}

run "external_key_data_source_apply" {
  module {
    source = "../examples/data-sources/external_key"
  }

  assert {
    condition     = output.external_key_provider == "aws"
    error_message = "Expected the mocked provider_config.type to reach the output."
  }

  assert {
    condition     = output.external_key_attachment == "attached"
    error_message = "Expected the mocked attachment to reach the output."
  }
}
