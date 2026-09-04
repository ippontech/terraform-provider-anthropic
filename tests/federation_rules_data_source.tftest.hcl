test {
  parallel = true
}

# anthropic_federation_rules requires a real org:admin OAuth bearer token,
# which is not available in CI. The fixture defers the data source read past
# plan (via depends_on on an unapplied resource) so this test only validates
# the schema, never making a live call.
run "federation_rules_data_source_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_rules_plan"
  }

  assert {
    condition     = output.federation_rules_issuer_id == "fdis_01placeholder"
    error_message = "Expected issuer_id to be 'fdis_01placeholder'."
  }

  assert {
    condition     = output.federation_rules_include_archived == true
    error_message = "Expected include_archived to be true."
  }
}
