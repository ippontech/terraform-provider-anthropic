test {
  parallel = true
}

# This validates schema only (command = plan). The fixture defers the data
# source read past plan via depends_on on a never-applied seed resource, since
# the dummy auth_token in the fixture provider block cannot make a live call.
# See tests/fixtures/service_account_plan/main.tf.
run "service_account_data_source_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/service_account_plan"
  }

  assert {
    condition     = output.service_account_id != ""
    error_message = "Expected service_account_id output to be non-empty."
  }
}
