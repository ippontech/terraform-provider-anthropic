test {
  parallel = true
}

# The user profiles beta returned a plain 404 for the terraform-tests
# organization's standard key when probed on 2026-09-30, regardless of beta
# header, so there is no way to apply this resource live. This test only
# plans the public example against a dummy standard credential.
provider "anthropic" {
  api_key = "dummy-api-key"
}

run "user_profile_plan" {
  command = plan

  module {
    source = "../examples/resources/user_profile"
  }

  assert {
    condition     = anthropic_user_profile.example.access_type == "application"
    error_message = "Expected access_type to be 'application'."
  }

  assert {
    condition     = anthropic_user_profile.example.external_id == "customer-4821"
    error_message = "Expected external_id to be 'customer-4821'."
  }

  assert {
    condition     = anthropic_user_profile.example.name == "Acme Corp"
    error_message = "Expected name to be 'Acme Corp'."
  }
}
